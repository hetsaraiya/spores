package slackhandler

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
)

// TestLiveSlackScopes reads the exact OAuth scopes granted to the bot token
// straight from Slack's response headers. chat:write is required to reply.
func TestLiveSlackScopes(t *testing.T) {
	if os.Getenv("SPORE_LIVE") != "1" {
		t.Skip("set SPORE_LIVE=1 to run the live Slack check")
	}
	_ = godotenv.Load("../.env")
	token := os.Getenv("SLACK_BOT_TOKEN")
	if token == "" {
		t.Fatal("SLACK_BOT_TOKEN not set")
	}
	resp, err := http.PostForm("https://slack.com/api/auth.test", url.Values{"token": {token}})
	if err != nil {
		t.Fatalf("auth.test call failed: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	scopes := resp.Header.Get("X-OAuth-Scopes")
	t.Logf("granted bot scopes: %s", scopes)
	if !strings.Contains(scopes, "chat:write") {
		t.Errorf("MISSING chat:write — the bot cannot post messages. This is why it never replies.")
	}
}

// TestLiveSlack exercises the real Slack API with the configured bot token to
// confirm the bot can authenticate and post. It is skipped unless SPORE_LIVE=1
// so it never runs in the normal test suite. Run with:
//
//	SPORE_LIVE=1 go test ./slackhandler/ -run TestLiveSlack -v
func TestLiveSlack(t *testing.T) {
	if os.Getenv("SPORE_LIVE") != "1" {
		t.Skip("set SPORE_LIVE=1 to run the live Slack check")
	}
	_ = godotenv.Load("../.env")

	botToken := os.Getenv("SLACK_BOT_TOKEN")
	if botToken == "" {
		t.Fatal("SLACK_BOT_TOKEN not set")
	}
	api := slack.New(botToken)

	auth, err := api.AuthTest()
	if err != nil {
		t.Fatalf("auth.test failed (token/scope problem): %v", err)
	}
	t.Logf("authenticated: team=%q user=%q bot_user_id=%s", auth.Team, auth.User, auth.UserID)

	channels, _, err := api.GetConversationsForUser(&slack.GetConversationsForUserParameters{
		Types: []string{"public_channel", "private_channel"},
		Limit: 200,
	})
	if err != nil {
		t.Fatalf("could not list bot channels (missing channels:read?): %v", err)
	}
	if len(channels) == 0 {
		t.Fatal("bot is not a member of ANY channel — it cannot post; invite it with /invite @spore")
	}
	for _, c := range channels {
		t.Logf("bot is in channel: #%s (%s)", c.Name, c.ID)
	}

	// Post to the first channel (or SPORE_TEST_CHANNEL if set) to prove the
	// full post path works end to end.
	target := os.Getenv("SPORE_TEST_CHANNEL")
	if target == "" {
		target = channels[0].ID
	}
	ts, _, err := api.PostMessage(target, slack.MsgOptionText(
		"👋 spore connectivity check: I can post to this channel. (automated test)", false))
	if err != nil {
		t.Fatalf("PostMessage to %s failed: %v", target, err)
	}
	t.Logf("posted test message to %s at ts=%s", target, ts)
}
