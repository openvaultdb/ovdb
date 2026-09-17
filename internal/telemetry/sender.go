package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Build-time values, set with -ldflags -X (see .goreleaser.yaml). A build
// without a key sends nothing and says "unavailable in this build".
var (
	posthogKey      string
	posthogEndpoint = "https://eu.i.posthog.com"
)

// Key is the build's PostHog project key, "" when unavailable.
func Key() string { return posthogKey }

// Available reports whether this build can send at all.
func Available() bool { return posthogKey != "" }

// Timeout bounds one batch send, connection included
// (REQ:bounded-synchronous-sender).
const Timeout = 2 * time.Second

// MaxBuffered is the pre-consent buffer's size (REQ:pre-consent-buffer).
const MaxBuffered = 100

// Recorder collects one process's events and sends them in one batch per
// command or step. A nil *Recorder records nothing.
type Recorder struct {
	// Channel is the sending interface; detected from Getenv and Environ
	// at send time when empty (cli or agent).
	Channel Channel
	Environ func() []string // os.Environ when nil
	Home    string
	Version string
	Getenv  func(string) string // os.Getenv when nil
	// Buffer keeps events in memory while not_asked (the TUI process),
	// for Flush after Turn on or Discard after No thanks. The CLI and the
	// server never buffer.
	Buffer bool
	// Key and Endpoint default to the build's values; tests set them.
	Key, Endpoint string
	Client        *http.Client

	mu      sync.Mutex
	pending []Event
}

func (r *Recorder) key() string {
	if r.Key != "" {
		return r.Key
	}
	return posthogKey
}

func (r *Recorder) endpoint() string {
	if r.Endpoint != "" {
		return r.Endpoint
	}
	return posthogEndpoint
}

// Available reports whether this recorder has a key to send with.
func (r *Recorder) Available() bool { return r.key() != "" }

// Decide is this recorder's process decision.
func (r *Recorder) Decide() Decision { return Decide(r.Home, r.Getenv, r.key()) }

// Record keeps e when it may be sent, or buffers it while not_asked when
// buffering; anything else is dropped on the spot.
func (r *Recorder) Record(events ...Event) {
	if r == nil || len(events) == 0 {
		return
	}
	d := r.Decide()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range events {
		switch {
		case d.Sending:
			r.pending = append(r.pending, e)
		case r.Buffer && d.State == StateNotAsked && len(r.pending) < MaxBuffered:
			r.pending = append(r.pending, e)
		}
	}
}

// Pending is how many events wait for a flush.
func (r *Recorder) Pending() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// Discard drops every pending event (No thanks, dismissal, exit).
func (r *Recorder) Discard() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.pending = nil
	r.mu.Unlock()
}

// Flush sends the pending events in one synchronous POST bounded by
// Timeout, when this process may send. A buffering recorder keeps its
// events while still not_asked; otherwise they are dropped. Failures are
// silent.
func (r *Recorder) Flush(ctx context.Context) {
	if r == nil {
		return
	}
	d := r.Decide()
	r.mu.Lock()
	events := r.pending
	if !d.Sending && r.Buffer && d.State == StateNotAsked {
		r.mu.Unlock()
		return
	}
	r.pending = nil
	r.mu.Unlock()
	if !d.Sending || len(events) == 0 {
		return
	}
	channel := r.Channel
	if channel == "" {
		channel = DetectChannel(r.Getenv, r.Environ)
	}
	r.send(ctx, events, Meta{Channel: channel, Version: r.Version, InstallID: d.Consent.InstallID})
}

type batchEvent struct {
	Event      Name           `json:"event"`
	Properties map[string]any `json:"properties"`
}

type batch struct {
	APIKey string       `json:"api_key"`
	Batch  []batchEvent `json:"batch"`
}

// Payload is the PostHog batch body for events.
func Payload(key string, events []Event, meta Meta) []byte {
	body := batch{APIKey: key, Batch: make([]batchEvent, 0, len(events))}
	for _, e := range events {
		body.Batch = append(body.Batch, batchEvent{Event: e.name, Properties: e.Properties(meta)})
	}
	data, _ := json.Marshal(body)
	return data
}

func (r *Recorder) send(ctx context.Context, events []Event, meta Meta) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint()+"/batch/", bytes.NewReader(Payload(r.key(), events, meta)))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	_ = response.Body.Close()
}
