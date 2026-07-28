package slackhandler

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hetsaraiya/spores/internal/agent"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

const (
	seenTTL       = 15 * time.Minute
	historySize   = 20
	maxImageBytes = 20 << 20
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
	go h.run(mention.Channel, mention.User, mention.TimeStamp, stripMention(mention.Text))
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

func (h *Handler) run(channel, userID, timestamp, message string) {
	ctx := context.Background()
	history, current := h.history(ctx, channel, timestamp)
	request := agent.Request{
		Speaker:   h.resolveName(ctx, userID),
		SpeakerID: userID,
		Message:   strings.TrimSpace(message),
		Images:    current.Images,
		History:   history,
	}
	result, err := h.agent.Run(ctx, request)
	if err != nil {
		h.post(channel, "❌ "+err.Error())
		return
	}
	if strings.TrimSpace(result) == "" {
		result = "(no response)"
	}
	h.post(channel, result)
}

func (h *Handler) history(ctx context.Context, channel, currentTimestamp string) ([]agent.Turn, agent.Turn) {
	response, err := h.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Limit:     historySize,
	})
	if err != nil {
		log.Printf("load Slack history: %v", err)
		return nil, agent.Turn{}
	}

	turns := make([]agent.Turn, 0, len(response.Messages))
	var current agent.Turn
	for index := len(response.Messages) - 1; index >= 0; index-- {
		message := response.Messages[index]
		isAssistant := h.botID != "" && message.User == h.botID
		if message.BotID != "" && !isAssistant {
			continue
		}
		if message.SubType != "" && message.SubType != slack.MsgSubTypeFileShare && !isAssistant {
			continue
		}
		turn := agent.Turn{
			Message:     strings.TrimSpace(message.Text),
			Images:      h.images(ctx, message.Files),
			IsAssistant: isAssistant,
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

		var contents bytes.Buffer
		if err := h.api.GetFileContext(ctx, downloadURL, &contents); err != nil {
			log.Printf("download Slack image %s (%s): %v", file.ID, file.Name, err)
			continue
		}
		images = append(images, "data:"+file.Mimetype+";base64,"+base64.StdEncoding.EncodeToString(contents.Bytes()))
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

func (h *Handler) post(channel, text string) {
	if _, _, err := h.api.PostMessage(channel, slack.MsgOptionText(text, false)); err != nil {
		log.Printf("post Slack response: %v", err)
	}
}

var mentionRE = regexp.MustCompile(`^\s*<@[A-Z0-9]+>\s*`)

func stripMention(text string) string { return mentionRE.ReplaceAllString(text, "") }
