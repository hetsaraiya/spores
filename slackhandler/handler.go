package slackhandler

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	"spore/agent"
	"spore/router"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type Handler struct {
	client    *socketmode.Client
	api       *slack.Client
	router    *router.Router
	lastEvent time.Time
	lastJob   time.Time
}

func New(botToken, appToken string, rt *router.Router) *Handler {
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	return &Handler{client: socketmode.New(api), api: api, router: rt, lastEvent: time.Now()}
}

func (h *Handler) Run() {
	log.Print("Slack handler ready. Waiting for /issue or an app mention.")
	log.Print("Try: /issue https://github.com/owner/repo/issues/123")
	log.Print("Or: @coder-spore https://github.com/owner/repo/issues/123")
	go h.heartbeat()
	go func() {
		for event := range h.client.Events {
			h.lastEvent = time.Now()
			log.Printf("slack event: %s data=%T", event.Type, event.Data)
			switch event.Type {
			case socketmode.EventTypeEventsAPI:
				h.handleMention(event)
			case socketmode.EventTypeSlashCommand:
				h.handleCommand(event)
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
	log.Printf("slack events_api callback inner=%T", apiEvent.InnerEvent.Data)
	mention, ok := apiEvent.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if ok {
		log.Printf("starting mention job channel=%s text=%q", mention.Channel, stripMention(mention.Text))
		go h.run(mention.Channel, stripMention(mention.Text))
		return
	}
	log.Printf("slack callback ignored: not app_mention inner=%T", apiEvent.InnerEvent.Data)
}

func (h *Handler) handleCommand(event socketmode.Event) {
	h.client.Ack(*event.Request)
	cmd, ok := event.Data.(slack.SlashCommand)
	if !ok {
		log.Printf("slack slash ignored: data=%T", event.Data)
		return
	}
	log.Printf("slack slash command received command=%s channel=%s text=%q", cmd.Command, cmd.ChannelID, cmd.Text)
	if cmd.Command != "/issue" {
		log.Printf("slack slash ignored: unsupported command=%s", cmd.Command)
		return
	}
	log.Printf("starting slash job channel=%s text=%q", cmd.ChannelID, cmd.Text)
	go h.run(cmd.ChannelID, cmd.Text)
}

func (h *Handler) run(channel, message string) {
	h.lastJob = time.Now()
	log.Printf("agent job started channel=%s message=%q", channel, strings.TrimSpace(message))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ctx = agent.WithStatus(ctx, func(msg string) {
		log.Print(msg)
	})
	result, err := h.router.Run(ctx, strings.TrimSpace(message))
	if err != nil {
		log.Printf("agent run failed: %v", err)
		h.post(channel, "❌ "+err.Error())
		return
	}
	h.post(channel, result)
	log.Printf("agent job finished channel=%s", channel)
}

func (h *Handler) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		idleFor := time.Since(h.lastEvent).Round(time.Second)
		jobMsg := "no Slack job received yet"
		if !h.lastJob.IsZero() {
			jobMsg = "last Slack job " + time.Since(h.lastJob).Round(time.Second).String() + " ago"
		}
		log.Printf("idle: connected and waiting for Slack input; last event %s ago; %s", idleFor, jobMsg)
	}
}

func (h *Handler) post(channel, text string) {
	if _, _, err := h.api.PostMessage(channel, slack.MsgOptionText(text, false)); err != nil {
		log.Printf("failed to post Slack message: %v", err)
	}
}

func stripMention(s string) string {
	return regexp.MustCompile(`^\s*<@[A-Z0-9]+>\s*`).ReplaceAllString(s, "")
}
