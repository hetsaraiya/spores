package slackhandler

import (
	"context"
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
	seenTTL     = 15 * time.Minute
	historySize = 20
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
	request := agent.Request{
		Speaker: h.resolveName(ctx, userID),
		Message: strings.TrimSpace(message),
		History: h.history(ctx, channel, timestamp),
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

func (h *Handler) history(ctx context.Context, channel, currentTimestamp string) []agent.Turn {
	response, err := h.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Limit:     historySize,
	})
	if err != nil {
		log.Printf("load Slack history: %v", err)
		return nil
	}

	turns := make([]agent.Turn, 0, len(response.Messages))
	for index := len(response.Messages) - 1; index >= 0; index-- {
		message := response.Messages[index]
		if message.Timestamp == currentTimestamp {
			continue
		}
		isAssistant := h.botID != "" && message.User == h.botID
		if message.BotID != "" && !isAssistant {
			continue
		}
		if message.SubType != "" && !isAssistant {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		turn := agent.Turn{Message: text, IsAssistant: isAssistant}
		if !isAssistant {
			turn.Message = strings.TrimSpace(stripMention(text))
			turn.Speaker = h.resolveName(ctx, message.User)
		}
		turns = append(turns, turn)
	}
	return turns
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
