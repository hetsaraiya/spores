package slackhandler

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hetsaraiya/spores/internal/agent"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

const (
	seenTTL     = 15 * time.Minute
	historySize = 20

	// Slack's declared file size is a hint, so the download is capped too.
	maxImageBytes = 20 << 20

	// Raw error text can carry a gateway's HTML page or an API response body.
	maxErrorChars = 300

	errorPrefix   = "❌ "
	emptyResponse = "(no response)"
	imageDataURL  = "data:%s;base64,%s"
	ellipsis      = "…"
)

type Handler struct {
	client *socketmode.Client
	api    *slack.Client
	agent  Responder
	botID  string

	seenMu  sync.Mutex
	seen    map[string]time.Time
	namesMu sync.Mutex
	names   map[string]string
}

type Responder interface {
	Run(context.Context, agent.Request) (string, error)
}

func New(botToken, appToken string, service Responder) (*Handler, error) {
	if strings.TrimSpace(botToken) == "" || strings.TrimSpace(appToken) == "" {
		return nil, fmt.Errorf("SLACK_BOT_TOKEN and SLACK_APP_TOKEN are required when PROMPT is not set")
	}
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	return &Handler{
		client: socketmode.New(api),
		api:    api,
		agent:  service,
		seen:   make(map[string]time.Time),
		names:  make(map[string]string),
	}, nil
}

// Run blocks while receiving Slack Socket Mode events.
func (h *Handler) Run() error {
	if auth, err := h.api.AuthTest(); err != nil {
		log.Printf("identify Slack bot: %v", err)
	} else {
		h.botID = auth.UserID
	}
	go func() {
		for event := range h.client.Events {
			if event.Type == socketmode.EventTypeEventsAPI {
				h.handleEvent(event)
			}
		}
	}()
	return h.client.Run()
}

func (h *Handler) handleEvent(event socketmode.Event) {
	if event.Request != nil {
		h.client.Ack(*event.Request)
	}
	apiEvent, ok := event.Data.(slackevents.EventsAPIEvent)
	if !ok || apiEvent.Type != slackevents.CallbackEvent {
		return
	}
	callback, ok := apiEvent.Data.(*slackevents.EventsAPICallbackEvent)
	if !ok || h.isDuplicate(callback.EventID) {
		return
	}
	mention, ok := apiEvent.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if !ok {
		return
	}
	go h.run(mention)
}

func (h *Handler) isDuplicate(eventID string) bool {
	if eventID == "" {
		return false
	}

	now := time.Now()
	h.seenMu.Lock()
	defer h.seenMu.Unlock()

	for id, seenAt := range h.seen {
		if now.Sub(seenAt) > seenTTL {
			delete(h.seen, id)
		}
	}
	if _, exists := h.seen[eventID]; exists {
		return true
	}
	h.seen[eventID] = now
	return false
}

// replyThread is the thread a mention belongs to, or the mention itself when it
// starts one. Always replying in-thread keeps conversations from interleaving.
func replyThread(mention *slackevents.AppMentionEvent) string {
	if mention.ThreadTimeStamp != "" {
		return mention.ThreadTimeStamp
	}
	return mention.TimeStamp
}

func (h *Handler) run(mention *slackevents.AppMentionEvent) {
	ctx := context.Background()
	threadTS := replyThread(mention)
	inThread := mention.ThreadTimeStamp != ""
	history, current := h.history(ctx, mention.Channel, threadTS, inThread, mention.TimeStamp)

	request := agent.Request{
		Speaker:   h.resolveName(ctx, mention.User),
		SpeakerID: mention.User,
		Message:   strings.TrimSpace(stripMention(mention.Text)),
		Images:    current.Images,
		History:   history,
	}
	result, err := h.agent.Run(ctx, request)
	if err != nil {
		h.post(mention.Channel, threadTS, errorPrefix+errorText(err))
		return
	}
	if strings.TrimSpace(result) == "" {
		result = emptyResponse
	}
	h.post(mention.Channel, threadTS, result)
}

// errorText flattens and bounds an error before it reaches a channel.
func errorText(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > maxErrorChars {
		return text[:maxErrorChars] + ellipsis
	}
	return text
}

// history loads prior context. A mention inside a thread reads that thread only;
// channel history there would pull in unrelated conversations and let concurrent
// threads contaminate each other.
func (h *Handler) history(ctx context.Context, channel, threadTS string, inThread bool, currentTimestamp string) ([]agent.Turn, agent.Turn) {
	messages, err := h.fetch(ctx, channel, threadTS, inThread)
	if err != nil {
		log.Printf("load Slack history: %v", err)
		return nil, agent.Turn{}
	}

	turns := make([]agent.Turn, 0, len(messages))
	var current agent.Turn
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		isAssistant := h.botID != "" && message.User == h.botID
		if message.BotID != "" && !isAssistant {
			continue
		}
		if message.SubType != "" && message.SubType != slack.MsgSubTypeFileShare && !isAssistant {
			continue
		}
		turn := agent.Turn{
			Message:     strings.TrimSpace(message.Text),
			IsAssistant: isAssistant,
		}
		// Assistant images are discarded downstream, so downloading them is waste.
		if !isAssistant {
			turn.Images = h.images(ctx, message.Files)
		}
		turn.Message += h.reactions(ctx, message.Reactions)
		if strings.TrimSpace(turn.Message) == "" && len(turn.Images) == 0 {
			continue
		}
		if !isAssistant {
			turn.Message = strings.TrimSpace(stripMention(turn.Message))
			turn.Speaker = h.resolveName(ctx, message.User)
		}
		if message.Timestamp == currentTimestamp {
			current = turn
			continue
		}
		turns = append(turns, turn)
	}
	return turns, current
}

// fetch reads the thread when in one, the channel otherwise; both newest-first.
func (h *Handler) fetch(ctx context.Context, channel, threadTS string, inThread bool) ([]slack.Message, error) {
	if inThread {
		messages, _, _, err := h.api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: channel,
			Timestamp: threadTS,
			Limit:     historySize,
		})
		if err != nil {
			return nil, err
		}
		slices.Reverse(messages) // Slack returns replies oldest-first
		return messages, nil
	}
	response, err := h.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Limit:     historySize,
	})
	if err != nil {
		return nil, err
	}
	return response.Messages, nil
}

// cappedBuffer refuses to grow past a limit, in case a file's declared size
// understates the real one.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		return 0, fmt.Errorf("image exceeds the %d-byte limit", c.limit)
	}
	return c.buf.Write(p)
}

func (h *Handler) images(ctx context.Context, files []slack.File) []string {
	var images []string
	for _, file := range files {
		if !strings.HasPrefix(file.Mimetype, "image/") {
			continue
		}
		downloadURL := file.URLPrivateDownload
		if downloadURL == "" {
			downloadURL = file.URLPrivate
		}
		if downloadURL == "" || file.Size > maxImageBytes {
			log.Printf("Slack image %s (%s) is unavailable or too large", file.ID, file.Name)
			continue
		}

		contents := &cappedBuffer{limit: maxImageBytes}
		if err := h.api.GetFileContext(ctx, downloadURL, contents); err != nil {
			log.Printf("download Slack image %s (%s): %v", file.ID, file.Name, err)
			continue
		}
		images = append(images, fmt.Sprintf(imageDataURL, file.Mimetype, base64.StdEncoding.EncodeToString(contents.buf.Bytes())))
	}
	return images
}

func (h *Handler) reactions(ctx context.Context, reactions []slack.ItemReaction) string {
	var result strings.Builder
	for _, reaction := range reactions {
		var users []string
		for _, userID := range reaction.Users {
			users = append(users, h.resolveName(ctx, userID))
		}
		fmt.Fprintf(&result, "\nReaction: :%s: ×%d", reaction.Name, reaction.Count)
		if len(users) > 0 {
			result.WriteString(" by " + strings.Join(users, ", "))
		}
	}
	return result.String()
}

func (h *Handler) resolveName(ctx context.Context, userID string) string {
	if userID == "" {
		return ""
	}
	h.namesMu.Lock()
	name, found := h.names[userID]
	h.namesMu.Unlock()
	if found {
		return name
	}

	name = userID
	if user, err := h.api.GetUserInfoContext(ctx, userID); err != nil {
		log.Printf("resolve Slack user %s: %v", userID, err)
	} else if user.Profile.DisplayName != "" {
		name = user.Profile.DisplayName
	} else if user.RealName != "" {
		name = user.RealName
	} else if user.Name != "" {
		name = user.Name
	}
	h.namesMu.Lock()
	h.names[userID] = name
	h.namesMu.Unlock()
	return name
}

func (h *Handler) post(channel, threadTS, text string) {
	options := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		options = append(options, slack.MsgOptionTS(threadTS))
	}
	if _, _, err := h.api.PostMessage(channel, options...); err != nil {
		log.Printf("post Slack response: %v", err)
	}
}

var mentionRE = regexp.MustCompile(`^\s*<@[A-Z0-9]+>\s*`)

func stripMention(text string) string { return mentionRE.ReplaceAllString(text, "") }
