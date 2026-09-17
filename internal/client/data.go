package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

// DataOp names one data API call for error mapping: what was attempted, on
// which database and path.
type DataOp struct {
	Verb     string // list, get, set, add, delete
	Database string
	Path     datapath.Path
	// Suffix makes next commands runnable as the command was: " --db todo"
	// when the database came from --db, "" otherwise.
	Suffix string
}

// DataRequest is one call to the openvaultdb-go data API (/v1).
type DataRequest struct {
	Op     DataOp
	Method string
	// URLPath is the request path under the server, e.g.
	// "/v1/databases/todo/records/lists/to-buy".
	URLPath string
	Body    any
	// Raw is sent as the body instead of Body, with ContentType.
	Raw         []byte
	ContentType string
}

// DatabaseURL is the data API path of database id.
func DatabaseURL(id string) string { return "/v1/databases/" + url.PathEscape(id) }

// RecordURL is the data API path of the record at path.
func RecordURL(id string, path datapath.Path) string {
	return DatabaseURL(id) + "/records/" + path.URLKey()
}

// Collections lists the collections at a database's root: GET
// /v1/databases/{db}, whose body is {"id","engine","schemaMode","collections"}.
func (l *Local) Collections(ctx context.Context, op DataOp, noStart bool) ([]byte, error) {
	return l.Data(ctx, DataRequest{Op: op, Method: http.MethodGet, URLPath: DatabaseURL(op.Database)}, noStart)
}

// Query lists the records of the collection at op.Path, at most limit (0:
// no limit): POST /v1/databases/{db}/query with collection and parent.
func (l *Local) Query(ctx context.Context, op DataOp, limit int, noStart bool) ([]byte, error) {
	body := map[string]any{"collection": op.Path.Name()}
	if parent := op.Path.Parent(); parent.Kind() != datapath.Root {
		body["parent"] = parent.Key()
	}
	if limit > 0 {
		body["limit"] = limit
	}
	return l.Data(ctx, DataRequest{Op: op, Method: http.MethodPost, URLPath: DatabaseURL(op.Database) + "/query", Body: body}, noStart)
}

// Page reads limit records of the collection at op.Path starting at offset.
// A root collection pages on the server with a DTQL offset; openvaultdb-go
// v0.6.0's DTQL takes root collections only, so a nested collection reads
// offset+limit records and drops the first offset (review F8; the query
// endpoint has no offset).
func (l *Local) Page(ctx context.Context, op DataOp, offset, limit int, noStart bool) ([]Record, error) {
	if op.Path.Parent().Kind() != datapath.Root || offset > 10000 {
		body, err := l.Query(ctx, op, offset+limit, noStart)
		if err != nil {
			return nil, err
		}
		var records Records
		if err := json.Unmarshal(body, &records); err != nil {
			return nil, err
		}
		return records.Records[min(offset, len(records.Records)):], nil
	}
	name, _ := json.Marshal(op.Path.Name()) // a JSON string is a YAML string
	doc := fmt.Sprintf("from: {name: %s}\nlimit: %d\noffset: %d\n", name, limit, offset)
	body, err := l.Data(ctx, DataRequest{Op: op, Method: http.MethodPost, URLPath: DatabaseURL(op.Database) + "/dtql",
		Raw: []byte(doc), ContentType: "application/yaml"}, noStart)
	if err != nil {
		return nil, err
	}
	var records Records
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, err
	}
	return records.Records, nil
}

// Get reads the record at op.Path.
func (l *Local) Get(ctx context.Context, op DataOp, noStart bool) ([]byte, error) {
	return l.Data(ctx, DataRequest{Op: op, Method: http.MethodGet, URLPath: RecordURL(op.Database, op.Path)}, noStart)
}

// MissingRecord reports whether err is a /v1 not_found for a record in a
// database that exists.
func MissingRecord(err error) bool {
	var v1 *V1Error
	return errors.As(err, &v1) && v1.Envelope.Code == envelope.NotFound && !bytes.Contains(v1.Body, []byte("database not found"))
}

// Records is the body of a query.
type Records struct {
	Records []Record `json:"records"`
}

// Record is the body of a record read, and one query result.
type Record struct {
	Key  string         `json:"key"`
	Data map[string]any `json:"data"`
}

// KeyID is the record id, unescaped, at the end of a key the server
// returned. A query in a nested collection returns keys without their parent
// ("items/k3f9x2"), so presentations build the full path from the collection
// they asked for and this id.
func KeyID(key string) string {
	if path, err := datapath.FromKey(key); err == nil {
		return path.Name()
	}
	return key[strings.LastIndex(key, "/")+1:]
}

// DatabaseInfo is the body of GET /v1/databases/{db}.
type DatabaseInfo struct {
	ID          string   `json:"id"`
	Engine      string   `json:"engine"`
	SchemaMode  string   `json:"schemaMode"`
	Collections []string `json:"collections"`
}

// V1Error is a failed /v1 call: Body is the /v1 error body byte for byte,
// printed unchanged with --json; the envelope it unwraps to is the same
// failure mapped for people (configuration-parity#REQ:error-envelope,
// database-context-navigation#REQ:server-errors-mapped).
type V1Error struct {
	Status   int
	Body     []byte
	Envelope *envelope.Error
}

func (e *V1Error) Error() string { return e.Envelope.Error() }

// Unwrap lets envelope.As find the human mapping.
func (e *V1Error) Unwrap() error { return e.Envelope }

// Data calls the data API with the instance secret, starting the server
// unless noStart (database-context-navigation#REQ:data-commands-use-server).
// A 2xx response returns its body; any other returns a *V1Error.
func (l *Local) Data(ctx context.Context, request DataRequest, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	var payload io.Reader
	if request.Raw != nil {
		payload = bytes.NewReader(request.Raw)
	} else if request.Body != nil {
		data, err := json.Marshal(request.Body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(data)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, runtime.BaseURL(c.state.Record.Port)+request.URLPath, payload)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.state.Secret)
	if request.Raw != nil {
		httpRequest.Header.Set("Content-Type", request.ContentType)
	} else if payload != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return nil, NotRunning().WithReason(err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusMultipleChoices {
		return body, nil
	}
	return nil, MapV1(response.StatusCode, body, request.Op)
}

type v1ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// v1Codes is the /v1-to-envelope code table of configuration parity.
var v1Codes = map[string]envelope.Code{
	"bad_request":               envelope.InvalidArgument,
	"invalid_dtql":              envelope.InvalidArgument,
	"invalid_grant":             envelope.Unauthorized,
	"forbidden":                 envelope.Forbidden,
	"access_denied":             envelope.Forbidden,
	"not_found":                 envelope.NotFound,
	"already_exists":            envelope.AlreadyExists,
	"not_supported":             envelope.Unsupported,
	"authorization_unsupported": envelope.Unsupported,
	"authorization_unavailable": envelope.StorageUnavailable,
	"internal":                  envelope.Internal,
}

// noSchemaDeclared is how openvaultdb-go's strict mode says a collection
// has no schema (schema.ValidateRecord).
const noSchemaDeclared = "no schema declared"

// MapV1 maps a /v1 failure to the envelope people see, with a next step.
// A body that is not a /v1 error keeps its bytes and maps to internal.
func MapV1(status int, body []byte, op DataOp) *V1Error {
	var parsed v1ErrorBody
	_ = json.Unmarshal(body, &parsed)
	v1Code, v1Message := strings.ToLower(parsed.Error.Code), parsed.Error.Message
	code, known := v1Codes[v1Code]
	switch {
	case v1Code == "schema_validation" && strings.Contains(v1Message, noSchemaDeclared):
		code = envelope.SchemaRequired
	case v1Code == "schema_validation":
		code = envelope.ValidationFailed
	case status == http.StatusUnauthorized:
		code = envelope.Unauthorized
	case !known:
		code = envelope.Internal
	}
	params := map[string]string{"database": op.Database, "path": op.Path.Display()}
	var message string
	switch op.Verb {
	case "list":
		message = uicopy.T("data.failed.list", params)
	case "get":
		message = uicopy.T("data.failed.get", params)
	case "add":
		message = uicopy.T("data.failed.add", params)
	case "delete":
		message = uicopy.T("data.failed.delete", params)
	default:
		message = uicopy.T("data.failed.set", params)
	}
	e := envelope.New(code, message)
	if v1Message != "" {
		e = e.WithReason(datapath.Printable(redact.String(v1Message)))
	} else if code == envelope.Internal {
		e = e.WithReason(fmt.Sprintf("HTTP %d", status))
	}
	collection := op.Path
	if collection.Kind() == datapath.Record {
		collection = collection.Parent()
	}
	switch {
	case code == envelope.NotFound && strings.HasPrefix(v1Message, "database not found"):
		e = e.WithReason(uicopy.T("context.unknown_database", map[string]string{"name": op.Database})).
			WithNext(envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases"},
				envelope.Next{Label: uicopy.T("next.use_database", nil), Command: "ovdb use <database>"})
	case code == envelope.NotFound:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.list_records", nil), Command: "ovdb list " + collection.Arg() + op.Suffix})
	case code == envelope.AlreadyExists:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.replace_record", nil), Command: "ovdb set " + op.Path.Arg() + " '<json>'" + op.Suffix})
	case code == envelope.SchemaRequired:
		e = e.WithNext(
			envelope.Next{Label: uicopy.T("next.describe_collection", map[string]string{"collection": datapath.Printable(collection.Name()), "database": op.Database}), Command: "ovdb databases reload " + op.Database},
			envelope.Next{Label: uicopy.T("next.see_databases", nil), Command: "ovdb databases"})
	case code == envelope.ValidationFailed:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.check_schema", nil), Command: "ovdb databases"})
	default:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.server_status", nil), Command: "ovdb server status"})
	}
	return &V1Error{Status: status, Body: body, Envelope: e}
}
