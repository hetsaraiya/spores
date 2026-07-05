package slackhandler

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"spore/agent"
	"spore/router"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// maxConcurrentJobs caps simultaneous agent runs; each one holds an E2B
// sandbox and a 15-minute budget, so unbounded fan-out gets expensive fast.
const maxConcurrentJobs = 2

// seenTTL is how long delivered event IDs are remembered for dedup. Slack
// retries events it considers unacknowledged, so the same event can arrive
// more than once.
const seenTTL = 15 * time.Minute

type Handler struct {
	client    *socketmode.Client
	api       *slack.Client
	router    *router.Router
	lastEvent atomic.Int64 // unix seconds; read by heartbeat goroutine
	lastJob   atomic.Int64
	botUserID string

	mu   sync.Mutex
	seen map[string]time.Time // event ID -> time seen, for deduplication

	namesMu sync.Mutex
	names   map[string]string // cache of Slack user ID -> display name

	jobs chan struct{} // semaphore for concurrent agent runs
}

func New(botToken, appToken string, rt *router.Router) *Handler {
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	h := &Handler{
		client: socketmode.New(api),
		api:    api,
		router: rt,
		seen:   make(map[string]time.Time),
		names:  make(map[string]string),
		jobs:   make(chan struct{}, maxConcurrentJobs),
	}
	h.lastEvent.Store(time.Now().Unix())
	return h
}

func (h *Handler) Run() {
	if auth, err := h.api.AuthTest(); err == nil {
		h.botUserID = auth.UserID
		log.Printf("bot identity: user_id=%s name=%s team=%s", auth.UserID, auth.User, auth.Team)
	} else {
		log.Printf("WARNING: auth.test failed; bot messages may be mislabeled in history: %v", err)
	}
	go h.heartbeat()
	go func() {
		for event := range h.client.Events {
			h.lastEvent.Store(time.Now().Unix())
			log.Printf("slack event: %s data=%T", event.Type, event.Data)
			switch event.Type {
			case socketmode.EventTypeEventsAPI:
				h.handleMention(event)
			case socketmode.EventTypeConnectionError:
				log.Printf("slack connection error: %+v", event.Data)
			default:
				log.Printf("slack event ignored: type=%s data=%T", event.Type, event.Data)
			}
		}
	}()
	if err := h.client.Run(); err != nil {
		log.Fatalf("Slack Socket Mode failed: %v", err)
	}
}

func (h *Handler) handleMention(event socketmode.Event) {
	h.client.Ack(*event.Request)
	apiEvent, ok := event.Data.(slackevents.EventsAPIEvent)
	if !ok || apiEvent.Type != slackevents.CallbackEvent {
		log.Printf("slack events_api ignored: ok=%t type=%q data=%T", ok, apiEvent.Type, event.Data)
		return
	}
	if cb, ok := apiEvent.Data.(*slackevents.EventsAPICallbackEvent); ok && h.alreadySeen(cb.EventID) {
		log.Printf("slack event deduped: event_id=%s", cb.EventID)
		return
	}
	log.Printf("slack events_api callback inner=%T", apiEvent.InnerEvent.Data)
	mention, ok := apiEvent.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if ok {
		go h.run(mention.Channel, h.resolveName(mention.User), stripMention(mention.Text))
		return
	}
	log.Printf("slack callback ignored: not app_mention inner=%T", apiEvent.InnerEvent.Data)
}

// alreadySeen records the event ID and reports whether it was seen within
// seenTTL. Empty IDs are never deduped.
func (h *Handler) alreadySeen(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, t := range h.seen {
		if now.Sub(t) > seenTTL {
			delete(h.seen, k)
		}
	}
	if _, dup := h.seen[id]; dup {
		return true
	}
	h.seen[id] = now
	return false
}

// jobBudget is the wall-clock limit for a single agent run. The watchdog in
// run posts a reply if the work hasn't finished a little past this, so the
// user always hears back even if a sandbox/Codex call ignores cancellation.
const jobBudget = 15 * time.Minute

// run executes one job. Progress emits stay in the logs; only the agent's
// final response (or failure) is posted to Slack. run guarantees exactly one
// reply to the channel for every job: success, error, timeout, or panic.
func (h *Handler) run(channel, speaker, message string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("agent job panicked channel=%s: %v\n%s", channel, r, debug.Stack())
			h.post(channel, "❌ I hit an unexpected internal error while working on this and had to stop. I've stayed online — please try again.")
		}
	}()

	select {
	case h.jobs <- struct{}{}:
		defer func() { <-h.jobs }()
	default:
		h.post(channel, "⏳ I'm already working on the maximum number of tasks. Please try again in a few minutes.")
		return
	}
	h.lastJob.Store(time.Now().Unix())
	log.Printf("agent job started channel=%s message=%q", channel, strings.TrimSpace(message))

	ctx, cancel := context.WithTimeout(context.Background(), jobBudget)
	defer cancel()
	ctx = agent.WithStatus(ctx, func(msg string) { log.Print(msg) })

	type outcome struct {
		result string
		err    error
	}
	// Buffered so the worker never blocks sending its result, even if the
	// watchdog already fired and we stopped listening.
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("agent worker panicked channel=%s: %v\n%s", channel, r, debug.Stack())
				done <- outcome{err: fmt.Errorf("internal error: %v", r)}
			}
		}()
		result, err := h.router.Run(ctx, channel, speaker, strings.TrimSpace(message))
		done <- outcome{result: result, err: err}
	}()

	// The watchdog fires slightly after the budget so a clean ctx-cancellation
	// (which returns via done with an error) wins the race and gives a better
	// message; the watchdog only catches work that ignored cancellation.
	watchdog := time.NewTimer(jobBudget + 30*time.Second)
	defer watchdog.Stop()

	select {
	case o := <-done:
		if o.err != nil {
			log.Printf("agent run failed channel=%s: %v", channel, o.err)
			h.post(channel, "❌ "+o.err.Error())
			return
		}
		if strings.TrimSpace(o.result) == "" {
			h.post(channel, "✅ Done — but I didn't get a summary back. Please check the repo for the result.")
			return
		}
		h.post(channel, o.result)
		log.Printf("agent job finished channel=%s", channel)
	case <-watchdog.C:
		log.Printf("agent job watchdog fired channel=%s; work exceeded budget", channel)
		h.post(channel, "⏱️ This task ran past my time budget so I had to stop waiting on it. Any issue or branch I created may be partially done — please check the repo. Try again with a narrower request if needed.")
	}
}

func (h *Handler) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		idleFor := time.Since(time.Unix(h.lastEvent.Load(), 0)).Round(time.Second)
		jobMsg := "no Slack job received yet"
		if lastJob := h.lastJob.Load(); lastJob != 0 {
			jobMsg = "last Slack job " + time.Since(time.Unix(lastJob, 0)).Round(time.Second).String() + " ago"
		}
		log.Printf("idle: connected and waiting for Slack input; last event %s ago; %s", idleFor, jobMsg)
	}
}

// post sends one message to the channel, retrying once on transient failure.
// A failure here is the last thing standing between the user and a reply, so
// it is logged loudly rather than silently dropped.
func (h *Handler) post(channel, text string) {
	if strings.TrimSpace(text) == "" {
		text = "(no content)"
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		ts, _, err := h.api.PostMessage(channel, slack.MsgOptionText(text, false))
		if err == nil {
			log.Printf("posted Slack message channel=%s ts=%s", channel, ts)
			return
		}
		lastErr = err
		log.Printf("failed to post Slack message (attempt %d) channel=%s: %v", attempt+1, channel, err)
	}
	log.Printf("ERROR: gave up posting Slack message channel=%s: %v", channel, lastErr)
}

func stripMention(s string) string {
	return regexp.MustCompile(`^\s*<@[A-Z0-9]+>\s*`).ReplaceAllString(s, "")
}

// historyFetchLimit is how many recent channel messages History pulls before
// filtering — enough to cover an active conversation without heavy API cost.
const historyFetchLimit = 60

// History fetches recent messages from the channel and shapes them into prior
// conversation turns for the router. Slack is the source of truth, so this
// carries no local state and survives redeploys. currentText is the message
// being handled right now; its matching (most recent) human turn is dropped so
// the router doesn't replay it on top of appending it. Implements
// router.HistoryFunc, and is wired in via Router.SetHistory.
func (h *Handler) History(ctx context.Context, channel, currentText string) []router.Turn {
	resp, err := h.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Limit:     historyFetchLimit,
	})
	if err != nil {
		log.Printf("conversation history failed channel=%s (need channels:history scope?): %v", channel, err)
		return nil
	}

	// Slack returns newest-first; walk oldest-first so turns read in order.
	msgs := resp.Messages
	turns := make([]router.Turn, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		// Skip join/leave/topic and other system noise, but keep real bot posts.
		if m.SubType != "" && m.SubType != "bot_message" {
			continue
		}
		isBot := h.botUserID != "" && m.User == h.botUserID
		text := strings.TrimSpace(m.Text)
		if !isBot {
			text = strings.TrimSpace(stripMention(m.Text))
		}
		if text == "" {
			continue
		}
		speaker := ""
		if !isBot {
			speaker = h.resolveName(m.User)
		}
		turns = append(turns, router.Turn{Speaker: speaker, IsBot: isBot, Text: text})
	}

	// Remove the message being handled now (its most recent human occurrence)
	// so it isn't duplicated when the router appends it.
	if current := strings.TrimSpace(currentText); current != "" {
		for i := len(turns) - 1; i >= 0; i-- {
			if !turns[i].IsBot && turns[i].Text == current {
				turns = append(turns[:i], turns[i+1:]...)
				break
			}
		}
	}
	return turns
}

// resolveName returns a display name for a Slack user ID, cached to avoid
// repeated users.info calls. Falls back to the raw ID if lookup fails (e.g.
// the users:read scope is missing).
func (h *Handler) resolveName(userID string) string {
	if userID == "" {
		return ""
	}
	h.namesMu.Lock()
	if name, ok := h.names[userID]; ok {
		h.namesMu.Unlock()
		return name
	}
	h.namesMu.Unlock()

	name := userID
	if u, err := h.api.GetUserInfo(userID); err == nil {
		switch {
		case u.Profile.DisplayName != "":
			name = u.Profile.DisplayName
		case u.RealName != "":
			name = u.RealName
		case u.Name != "":
			name = u.Name
		}
	} else {
		log.Printf("users.info failed for %s (need users:read scope?): %v", userID, err)
	}

	h.namesMu.Lock()
	h.names[userID] = name
	h.namesMu.Unlock()
	return name
}
