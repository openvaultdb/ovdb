package envelope

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The goldens pin the exact bytes agents parse. Field order, omitted
// optionals, an always-present next array and the trailing newline are all
// part of the contract (configuration-parity#AC:error-envelope-shape).
func TestMarshalErrorGoldens(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "minimal has empty next and no reason",
			err:  New(Unauthorized, "Sign in first"),
			want: `{"schema":1,"error":{"code":"unauthorized","message":"Sign in first","next":[]}}` + "\n",
		},
		{
			name: "full with command and action",
			err: New(PortInUse, "Couldn't start the OVDB server").
				WithReason("Port 6832 is already used by another program.").
				WithNext(
					Next{Label: "Use port 6833 instead", Command: "ovdb server start --port 6833", Action: "use_port"},
					Next{Label: "Keep port 6833 for next time", Command: "ovdb config set server.port 6833"},
				),
			want: `{"schema":1,"error":{"code":"port_in_use","message":"Couldn't start the OVDB server",` +
				`"reason":"Port 6832 is already used by another program.","next":[` +
				`{"label":"Use port 6833 instead","command":"ovdb server start --port 6833","action":"use_port"},` +
				`{"label":"Keep port 6833 for next time","command":"ovdb config set server.port 6833"}]}}` + "\n",
		},
		{
			name: "html characters are not escaped",
			err:  New(InvalidArgument, "a <b> & c"),
			want: `{"schema":1,"error":{"code":"invalid_argument","message":"a <b> & c","next":[]}}` + "\n",
		},
		{
			name: "nil next still renders an array",
			err:  &Error{Code: Internal, Message: "x"},
			want: `{"schema":1,"error":{"code":"internal","message":"x","next":[]}}` + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(MarshalError(tc.err)); got != tc.want {
				t.Errorf("MarshalError:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	in := New(ServerStartFailed, "m").WithReason("r").WithNext(Next{Label: "l", Command: "c"})
	out := Decode(MarshalError(in))
	if out == nil || out.Code != in.Code || out.Reason != "r" || len(out.Next) != 1 || out.Next[0].Command != "c" {
		t.Fatalf("Decode round trip = %+v", out)
	}
	for _, notEnvelope := range []string{``, `{}`, `{"schema":1}`, `{"error":{"code":"x"}}`, `[1]`} {
		if got := Decode([]byte(notEnvelope)); got != nil {
			t.Errorf("Decode(%q) = %+v, want nil", notEnvelope, got)
		}
	}
}

func TestHTTPStatus(t *testing.T) {
	want := map[Code]int{
		InvalidArgument: 400, ConfirmationRequired: 400, Unauthorized: 401, Forbidden: 403,
		NotFound: 404, AlreadyExists: 409, LocationNotEmpty: 409, ServerVersionMismatch: 409,
		SchemaRequired: 422, ValidationFailed: 422, Unsupported: 501, StorageUnavailable: 503,
		Internal: 500, PortInUse: 500, PortUnavailable: 500, ServerNotRunning: 500,
		ServerStartFailed: 500, ServerConfigMismatch: 500, DependencyMissing: 500,
	}
	if len(want) != len(Codes) {
		t.Fatalf("status table covers %d codes, closed list has %d", len(want), len(Codes))
	}
	for _, code := range Codes {
		if got := code.HTTPStatus(); got != want[code] {
			t.Errorf("%s.HTTPStatus() = %d, want %d", code, got, want[code])
		}
	}
}

func TestAsAndWrite(t *testing.T) {
	inner := New(Forbidden, "no")
	if got := As(fmt.Errorf("wrapped: %w", inner)); got != inner {
		t.Errorf("As(wrapped) = %v", got)
	}
	if As(errors.New("plain")) != nil {
		t.Error("As(plain) should be nil")
	}
	if inner.Error() != "no" || New(Forbidden, "no").WithReason("why").Error() != "no: why" {
		t.Error("Error() text")
	}

	rec := httptest.NewRecorder()
	Write(rec, inner)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Write status/header = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != string(MarshalError(inner)) {
		t.Errorf("Write body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]int{"schema": 1})
	if rec.Body.String() != `{"schema":1}`+"\n" {
		t.Errorf("WriteJSON body = %q", rec.Body.String())
	}
}
