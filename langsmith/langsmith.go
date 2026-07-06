// Package langsmith traces to LangSmith in two layers: LLM calls via the official
// traceopenai HTTP middleware (native message/tool/usage mapping), and chain/tool
// runs via plain OTel spans (Start/End). LLM spans nest under chain spans through ctx.
package langsmith

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	lsgo "github.com/langchain-ai/langsmith-go"
	"github.com/langchain-ai/langsmith-go/instrumentation/traceopenai"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Tracer struct {
	otel    *lsgo.OTelTracer
	tracer  trace.Tracer
	enabled bool
}

// New returns a Tracer; an empty apiKey yields a no-op. Config comes from the caller, not the env.
func New(apiKey, project string) *Tracer {
	if apiKey == "" {
		return &Tracer{}
	}
	ot, err := lsgo.NewOTelTracer(
		lsgo.WithAPIKey(apiKey),
		lsgo.WithProjectName(project),
		lsgo.WithServiceName("spore"),
	)
	if err != nil {
		log.Printf("langsmith init failed, tracing disabled: %v", err)
		return &Tracer{}
	}
	log.Printf("langsmith tracing enabled: project=%s", project)
	return &Tracer{otel: ot, tracer: ot.Tracer("spore"), enabled: true}
}

func (t *Tracer) Enabled() bool { return t != nil && t.enabled }

// WrapHTTPClient wraps base with the LangSmith OpenAI middleware so every
// /chat/completions call becomes an LLM run. Returns base untouched when disabled. Nil-safe.
func (t *Tracer) WrapHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	if !t.Enabled() {
		return base
	}
	return traceopenai.WrapClient(base, traceopenai.WithTracerProvider(t.otel.TracerProvider()))
}

// Detach keeps ctx's trace span but drops its deadline/cancellation, for background
// work (post-turn memory update) that outlives the request yet stays in its trace.
func Detach(ctx context.Context) context.Context {
	return trace.ContextWithSpan(context.Background(), trace.SpanFromContext(ctx))
}

// Shutdown flushes buffered spans; call before a short-lived process exits.
func (t *Tracer) Shutdown(ctx context.Context) {
	if t.Enabled() {
		_ = t.otel.Shutdown(ctx)
	}
}

// Run wraps one OTel span. Methods are nil-safe (Start returns nil when disabled).
type Run struct {
	span trace.Span
}

// Start opens a chain/tool span nested under ctx's span; inputs render as the run's input JSON.
func (t *Tracer) Start(ctx context.Context, name, kind string, inputs map[string]any) (context.Context, *Run) {
	if !t.Enabled() {
		return ctx, nil
	}
	attrs := []attribute.KeyValue{
		attribute.String("langsmith.span.kind", kind),
	}
	if s := jsonAttr(inputs); s != "" {
		attrs = append(attrs, attribute.String("gen_ai.prompt", s))
	}
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, &Run{span: span}
}

// End closes the run with outputs and/or error. Nil-safe.
func (r *Run) End(outputs map[string]any, err error) {
	if r == nil || r.span == nil {
		return
	}
	if len(outputs) > 0 {
		if s := jsonAttr(outputs); s != "" {
			r.span.SetAttributes(attribute.String("gen_ai.completion", s))
		}
	}
	if err != nil {
		r.span.RecordError(err)
		r.span.SetStatus(codes.Error, err.Error())
	}
	r.span.End()
}

func jsonAttr(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
