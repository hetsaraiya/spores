package agent

import (
	"context"
	"fmt"

	"spore/githubclient"
)

type Agent struct {
	github     *githubclient.Client
	e2bKey     string
	codexModel string
	codexAuth  string
	openAIKey  string
}

type issueDraft struct {
	Repo   string   `json:"repo"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
	Hint   string   `json:"implementation_hint"`
}

type change struct {
	Output string
}

type StatusFunc func(string)

type statusKey struct{}

func New(gh *githubclient.Client, e2bKey, codexModel, codexAuth, openAIKey string) *Agent {
	return &Agent{
		github:     gh,
		e2bKey:     e2bKey,
		codexModel: codexModel,
		codexAuth:  codexAuth,
		openAIKey:  openAIKey,
	}
}

func WithStatus(ctx context.Context, fn StatusFunc) context.Context {
	return context.WithValue(ctx, statusKey{}, fn)
}

func (a *Agent) Run(ctx context.Context, message string) (string, error) {
	emit(ctx, "1/9 Starting E2B sandbox...")
	sb, err := a.spinSandbox(ctx)
	if err != nil {
		return "", fail(1, err)
	}
	defer func() { _ = sb.Close() }()
	if err = sb.ProbeIO(); err != nil {
		return "", fail(1, err)
	}
	if err = sb.SetupCodexAuth(a.codexAuth, a.openAIKey); err != nil {
		return "", fail(1, err)
	}
	issue, number, issueURL, exists, err := a.existingIssue(ctx, message)
	if err != nil {
		return "", fail(2, err)
	}
	if !exists {
		emit(ctx, "2/9 Parsing issue request with Codex...")
		issue, err = a.parseIssue(sb, message)
		if err != nil {
			return "", fail(2, err)
		}
		emit(ctx, "3/9 Creating GitHub issue...")
		number, issueURL, err = a.createIssue(ctx, issue)
		if err != nil {
			return "", fail(3, err)
		}
	} else {
		emit(ctx, "2/9 Using linked GitHub issue: "+issueURL)
	}
	emit(ctx, "📋 Issue created: "+issueURL)
	emit(ctx, "⚙️ Sandbox ready, cloning repo...")
	branch, err := a.cloneRepo(sb, issue.Repo, number, issue.Title)
	if err != nil {
		return "", fail(4, err)
	}
	emit(ctx, "5/9 Codex is implementing the issue...")
	changes, err := a.implementFix(sb, issue, "")
	if err != nil {
		return "", fail(5, err)
	}
	emit(ctx, "6/9 Validating Codex repository changes...")
	buildErr := a.applyChanges(sb, changes)
	if buildErr != nil {
		emit(ctx, "6/9 Codex validation failed; retrying once...")
		changes, err = a.implementFix(sb, issue, buildErr.Error())
		if err != nil {
			return "", fail(6, err)
		}
		buildErr = a.applyChanges(sb, changes)
	}
	if buildErr != nil {
		return "", fail(6, buildErr)
	}
	emit(ctx, "7/9 Committing and pushing branch...")
	if err = a.commitPush(sb, branch, number, issue.Title); err != nil {
		return "", fail(7, err)
	}
	emit(ctx, "8/9 Opening pull request...")
	prURL, err := a.openPR(ctx, issue.Repo, branch, number, issue.Title)
	if err != nil {
		return "", fail(8, err)
	}
	return fmt.Sprintf("✅ Done!\n📋 Issue: %s\n🔀 PR: %s", issueURL, prURL), nil
}

func emit(ctx context.Context, msg string) {
	if fn, ok := ctx.Value(statusKey{}).(StatusFunc); ok {
		fn(msg)
	}
}

// Emit reports a progress message through the status func set with WithStatus.
// Exported so the router can share the same status channel.
func Emit(ctx context.Context, msg string) {
	emit(ctx, msg)
}
