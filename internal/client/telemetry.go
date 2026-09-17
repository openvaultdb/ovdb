package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/runtime"
	"github.com/openvaultdb/ovdb/internal/setup"
	"github.com/openvaultdb/ovdb/internal/setup/demo"
	"github.com/openvaultdb/ovdb/internal/setup/explore"
	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// TelemetryPath is the consent state; TelemetryEventsPath takes the web
// page's buffered events.
const (
	TelemetryPath       = "/api/local/v1/telemetry"
	TelemetryEventsPath = "/api/local/v1/telemetry/events"
)

// Capability instrumentation (telemetry-consent#REQ:closed-event-set). The
// CLI and TUI both reach every onboarding capability through Local, so
// events are recorded once, here, in the process where the action
// happened; the recorder decides whether they are kept. Only enums,
// booleans, durations and envelope codes leave these helpers.

func (l *Local) failed(step string, err error) {
	if err != nil {
		l.Telemetry.Record(telemetry.NewOnboardingError(step, err))
	}
}

func (l *Local) trackStart(start time.Time, err *error) {
	l.Telemetry.Record(telemetry.NewServerStarted(*err == nil, l.Port == runtime.DefaultPort, time.Since(start)))
	l.failed("server", *err)
}

func (l *Local) trackCreate(start time.Time, request *setup.CreateRequest, err *error) {
	l.Telemetry.Record(telemetry.NewDatabaseCreated(request.Engine, *err == nil, time.Since(start)))
	l.failed("create", *err)
}

func (l *Local) trackConnect(start time.Time, request *setup.ConnectRequest, body *[]byte, err *error) {
	engine := request.Engine
	var result setup.DatabaseResult
	if *err == nil && json.Unmarshal(*body, &result) == nil && result.Database.Engine != "" {
		engine = result.Database.Engine
	}
	l.Telemetry.Record(telemetry.NewDatabaseConnected(engine, *err == nil, time.Since(start)))
	l.failed("connect", *err)
}

func (l *Local) trackDemoInstall(body *[]byte, err *error) {
	var document demo.Document
	already := *err == nil && json.Unmarshal(*body, &document) == nil && document.AlreadyInstalled
	l.Telemetry.Record(telemetry.NewDemoInstalled(*err == nil, already))
	l.failed("demo", *err)
}

func (l *Local) trackDemoOpen(err *error) {
	l.Telemetry.Record(telemetry.NewDemoOpened(*err == nil))
	l.failed("demo", *err)
}

func (l *Local) trackDataTugCLI(body *[]byte, err *error) {
	var document explore.DataTugCLI
	found := *err == nil && json.Unmarshal(*body, &document) == nil && document.OnPath
	l.Telemetry.Record(telemetry.NewExploreDataSelected("datatug_cli", found))
	l.failed("explore", *err)
}

// TelemetryStatus is this process's telemetry document: consent from
// config.yaml, forced-off conditions and key availability evaluated here,
// never in the server (REQ:sender-process-decides). It starts nothing.
func (l *Local) TelemetryStatus() telemetry.Document {
	if l.Telemetry != nil {
		return telemetry.NewDocument(l.Telemetry.Decide(), l.Telemetry.Available())
	}
	return telemetry.NewDocument(telemetry.Decide(l.Dirs.Home, nil, telemetry.Key()), telemetry.Available())
}

// SetTelemetry records a person's decision through the running server, or
// under the home lock when none runs, as SetConfig does. Turning it on
// records telemetry_consent_changed in this process. The document returned
// is evaluated in this process.
func (l *Local) SetTelemetry(ctx context.Context, change telemetry.Change) (telemetry.Document, error) {
	if l.Telemetry != nil {
		change.Channel = string(l.Telemetry.Channel)
		if change.Channel == "" {
			change.Channel = string(telemetry.DetectChannel(l.Telemetry.Getenv, l.Telemetry.Environ))
		}
	}
	changed, backup := false, ""
	for attempt := 0; ; attempt++ {
		state, err := l.inspect(ctx, false)
		if err != nil {
			return telemetry.Document{}, err
		}
		if state.Running {
			response, err := l.newClient(state).Do(ctx, http.MethodPut, TelemetryPath, change)
			if err != nil {
				return telemetry.Document{}, err
			}
			var document telemetry.Document
			if err := json.Unmarshal(response.Body, &document); err != nil {
				return telemetry.Document{}, err
			}
			changed, backup = document.Changed != nil && *document.Changed, document.Backup
			break
		}
		warnings, err := runtime.WithHomeLock(ctx, l.Dirs, uicopy.T("telemetry.failed", nil), func() error {
			outcome, applyErr := setup.ChangeTelemetry(l.Dirs, change, telemetry.ParseChannel(change.Channel), time.Now())
			changed, backup = outcome.Changed, outcome.Backup
			return applyErr
		})
		for _, warning := range warnings {
			l.notice(warning)
		}
		if errors.Is(err, runtime.ErrAlreadyRunning) && attempt == 0 {
			continue
		}
		if err != nil {
			return telemetry.Document{}, err
		}
		break
	}
	if change.State == telemetry.StateEnabled {
		// This session's Turn on releases what it buffered
		// (REQ:pre-consent-buffer).
		l.Telemetry.Consented()
		if changed {
			l.Telemetry.Record(telemetry.NewConsentEnabled())
		}
	}
	document := l.TelemetryStatus()
	document.Changed, document.Backup = &changed, backup
	return document, nil
}
