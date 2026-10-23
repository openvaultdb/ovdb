package localserver

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// loggedErrorKey marks a request whose internal error the data server logs
// should also be kept for the request itself.
type loggedErrorKey struct{}

// loggedError is where captureErrors keeps a request's logged error text.
type loggedError struct {
	mu    sync.Mutex
	parts []string
}

func (e *loggedError) add(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.parts = append(e.parts, text)
}

func (e *loggedError) text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.parts, "\n")
}

// captureErrors wraps the data server's logger: records still go to next,
// and the "error" attribute of a record logged for a request carrying a
// loggedError is kept there, so the local server can tell why openvaultdb-go
// answered a bare 500.
func captureErrors(next slog.Handler) slog.Handler { return &captureHandler{next: next} }

type captureHandler struct{ next slog.Handler }

func (h *captureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return ctx.Value(loggedErrorKey{}) != nil || h.next.Enabled(ctx, level)
}

func (h *captureHandler) Handle(ctx context.Context, record slog.Record) error {
	if slot, ok := ctx.Value(loggedErrorKey{}).(*loggedError); ok {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "error" {
				slot.add(attr.Value.String())
			}
			return true
		})
	}
	if !h.next.Enabled(ctx, record.Level) {
		return nil
	}
	return h.next.Handle(ctx, record)
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{next: h.next.WithAttrs(attrs)}
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	return &captureHandler{next: h.next.WithGroup(name)}
}

// gitIdentityMarkers are how git says it has no name or email to commit
// with (git's English messages; ovdb runs git with the server's locale).
var gitIdentityMarkers = []string{
	"Author identity unknown", "Committer identity unknown", "Please tell me who you are",
	"empty ident name", "unable to auto-detect email address",
}

// gitIdentityFailure reports whether an internal error is git refusing a
// commit for want of a name and email, and nothing else.
func gitIdentityFailure(text string) bool {
	for _, marker := range gitIdentityMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
