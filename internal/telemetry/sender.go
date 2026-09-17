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

// UserAgent is the only client header ovdb sets: no version, OS user or
// host name.
const UserAgent = "ovdb"

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
	pending []pending
}

// pending is one recorded event. buffered marks an event recorded while
// not_asked: it is sent only after this session's own Turn on (Consented),
// never because consent appeared on disk from another process.
type pending struct {
	event    Event
	buffered bool
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
			r.pending = append(r.pending, pending{event: e})
		case r.Buffer && d.State == StateNotAsked && r.buffered() < MaxBuffered:
			r.pending = append(r.pending, pending{event: e, buffered: true})
		}
	}
}

func (r *Recorder) buffered() int {
	n := 0
	for _, p := range r.pending {
		if p.buffered {
			n++
		}
	}
	return n
}

// Consented is this session's own Turn on: the events it buffered while
// not_asked may now be sent, by the next Flush.
func (r *Recorder) Consented() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.pending {
		r.pending[i].buffered = false
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

// Exit ends a buffering process's session (No thanks, dismissal and exit
// all end the same way): events buffered without this session's Turn on are
// dropped, even when another process enabled telemetry meanwhile; the rest
// is sent when this process may send.
func (r *Recorder) Exit(ctx context.Context) {
	r.Flush(ctx)
	r.Discard()
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
	var events []Event
	var kept []pending
	for _, p := range r.pending {
		switch {
		case p.buffered:
			// Only Consented releases these; Discard drops them.
			kept = append(kept, p)
		case d.Sending:
			events = append(events, p.event)
		}
	}
	r.pending = kept
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
	request.Header.Set("User-Agent", UserAgent)
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
