// Package langsmith wires LangSmith tracing in two layers:
//
//   - LLM calls are traced by the official traceopenai HTTP middleware
//     (WrapHTTPClient), which parses the real OpenAI request/response —
//     messages, roles, tool calls, token usage — so LangSmith renders them
//     natively. Nothing is hand-mapped.
//   - Chain and tool runs (router turn, memory update, tool dispatch) are
//     plain OTel spans opened with Start/End; LLM spans nest under them
//     automatically because they share one tracer provider via ctx.
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

// Run kinds, passed to Start. They map to LangSmith run types.
const (
	KindChain = "chain"
	KindTool  = "tool"
)

type Tracer struct {
	otel    *lsgo.OTelTracer
	tracer  trace.Tracer
	enabled bool
}

// New builds a Tracer. It always returns a non-nil Tracer; with an empty API
// key it is a no-op. Configuration comes from the caller (spore/config), not
// the environment.
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

// Enabled reports whether spans will actually be recorded.
func (t *Tracer) Enabled() bool { return t != nil && t.enabled }

// WrapHTTPClient wraps an HTTP client with the official LangSmith OpenAI
// middleware, which turns every /chat/completions call into a properly
// rendered LLM run (messages, tool calls, usage). With tracing disabled the
// client is returned untouched. Safe on a nil Tracer.
func (t *Tracer) WrapHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	if !t.Enabled() {
		return base
	}
	return traceopenai.WrapClient(base, traceopenai.WithTracerProvider(t.otel.TracerProvider()))
}

// Detach returns a background context that keeps ctx's trace span — so spans
// started from it nest in the SAME trace — but drops its deadline and
// cancellation. Use it for background work (e.g. the post-turn memory update)
// that outlives the request but should still appear under the turn's trace.
func Detach(ctx context.Context) context.Context {
	return trace.ContextWithSpan(context.Background(), trace.SpanFromContext(ctx))
}

// Shutdown flushes any buffered spans. Call before exiting a short-lived process.
func (t *Tracer) Shutdown(ctx context.Context) {
	if t.Enabled() {
		_ = t.otel.Shutdown(ctx)
	}
}

// Run wraps one OpenTelemetry span. Methods are safe on a nil Run (returned when
// the tracer is disabled).
type Run struct {
	span trace.Span
}

// Start opens a chain/tool span, nested under any span already in ctx.
// inputs are rendered as the run's input JSON. Returns a ctx carrying the new
// span so descendants — including middleware-traced LLM calls — nest under it.
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

// End closes the run with its outputs and/or error. Safe on a nil Run.
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
