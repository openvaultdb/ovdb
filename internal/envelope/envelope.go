// Package envelope is the one error shape every ovdb interface shares:
// the local API writes it, the CLI prints it (as JSON with --json, or as the
// "Couldn't …/Why/What you can do" problem pattern otherwise), and later the
// TUI and web console render it. Presentations never build one themselves;
// services and the shared client do.
//
// See spec/features/configuration-parity#REQ:error-envelope and
// spec/features/first-run-onboarding#REQ:problem-pattern.
package envelope

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

// Schema is the version of every local API body and --json document.
const Schema = 1

// Code is one of the closed set of error codes.
type Code string

// The closed list of codes. Adding one is a spec change.
const (
	InvalidArgument       Code = "invalid_argument"
	ConfirmationRequired  Code = "confirmation_required"
	NotFound              Code = "not_found"
	AlreadyExists         Code = "already_exists"
	LocationNotEmpty      Code = "location_not_empty"
	PortInUse             Code = "port_in_use"
	PortUnavailable       Code = "port_unavailable"
	ServerNotRunning      Code = "server_not_running"
	ServerStartFailed     Code = "server_start_failed"
	ServerVersionMismatch Code = "server_version_mismatch"
	ServerConfigMismatch  Code = "server_config_mismatch"
	Unauthorized          Code = "unauthorized"
	Forbidden             Code = "forbidden"
	StorageUnavailable    Code = "storage_unavailable"
	SchemaRequired        Code = "schema_required"
	ValidationFailed      Code = "validation_failed"
	Unsupported           Code = "unsupported"
	DependencyMissing     Code = "dependency_missing"
	Internal              Code = "internal"
)

// Codes lists every valid code, in the spec's order.
var Codes = []Code{
	InvalidArgument, ConfirmationRequired, NotFound, AlreadyExists, LocationNotEmpty,
	PortInUse, PortUnavailable, ServerNotRunning, ServerStartFailed, ServerVersionMismatch,
	ServerConfigMismatch, Unauthorized, Forbidden, StorageUnavailable, SchemaRequired,
	ValidationFailed, Unsupported, DependencyMissing, Internal,
}

// HTTPStatus is the local API status for code. Client-side-only codes
// (port_in_use, server_start_failed, …) never cross HTTP; they map to 500 so
// a misuse is loud rather than silently successful.
func (c Code) HTTPStatus() int {
	switch c {
	case InvalidArgument, ConfirmationRequired:
		return http.StatusBadRequest
	case Unauthorized:
		return http.StatusUnauthorized
	case Forbidden:
		return http.StatusForbidden
	case NotFound:
		return http.StatusNotFound
	case AlreadyExists, LocationNotEmpty, ServerVersionMismatch:
		return http.StatusConflict
	case SchemaRequired, ValidationFailed:
		return http.StatusUnprocessableEntity
	case Unsupported:
		return http.StatusNotImplemented
	case StorageUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Next is one thing the person (or agent) can do about a result or failure.
// Command is always runnable as written; Action names an in-UI remedy such as
// "use_port" that a TUI or web console offers as a button.
type Next struct {
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
	Action  string `json:"action,omitempty"`
}

// Error is a failure in envelope form. It is a Go error, so services return
// it through ordinary error paths and presentations recover it with As.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
	Next    []Next `json:"next"`
}

// New returns an Error with code and message and an empty next list.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message, Next: []Next{}}
}

// WithReason sets the "Why" line and returns e for chaining.
func (e *Error) WithReason(reason string) *Error {
	e.Reason = reason
	return e
}

// WithNext appends next actions and returns e for chaining.
func (e *Error) WithNext(next ...Next) *Error {
	e.Next = append(e.Next, next...)
	return e
}

func (e *Error) Error() string {
	if e.Reason != "" {
		return e.Message + ": " + e.Reason
	}
	return e.Message
}

// As returns the *Error in err's chain, or nil.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// body is the document written for a failure.
type body struct {
	Schema int    `json:"schema"`
	Error  *Error `json:"error"`
}

// Marshal renders any schema-1 document as compact JSON followed by one
// newline. The local API and the CLI both use it, which is what makes a
// command's --json output byte-for-byte equal to the API response body
// (configuration-parity#REQ:json-equals-api).
func Marshal(v any) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		// Every document is built from plain structs of strings, numbers and
		// slices; failing to encode one is a programming error.
		panic("envelope: encoding a schema document: " + err.Error())
	}
	return buf.Bytes()
}

// MarshalError renders e as the failure document.
func MarshalError(e *Error) []byte {
	if e.Next == nil {
		e.Next = []Next{}
	}
	return Marshal(body{Schema: Schema, Error: e})
}

// Decode parses a failure document, returning nil when data is not one.
func Decode(data []byte) *Error {
	var b body
	if err := json.Unmarshal(data, &b); err != nil || b.Schema != Schema || b.Error == nil || b.Error.Code == "" {
		return nil
	}
	if b.Error.Next == nil {
		b.Error.Next = []Next{}
	}
	return b.Error
}

// Write sends e as an HTTP response with the status its code maps to.
func Write(w http.ResponseWriter, e *Error) {
	WriteStatus(w, e.Code.HTTPStatus(), e)
}

// WriteStatus sends e with an explicit status, for the few responses whose
// status is dictated by a route rather than by the code.
func WriteStatus(w http.ResponseWriter, status int, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(MarshalError(e))
}

// WriteJSON sends a schema-1 success document.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(Marshal(v))
}
