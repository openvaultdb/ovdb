package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/redact"
)

// TokensPath is openvaultdb-go's token administration endpoint.
const TokensPath = "/v1/tokens"

// TokenRequest is the body of POST /v1/tokens.
type TokenRequest struct {
	Label        string   `json:"label,omitempty"`
	DatabaseID   string   `json:"databaseId,omitempty"`
	Capabilities []string `json:"capabilities"`
	ExpiresIn    string   `json:"expiresIn,omitempty"` // a Go duration; empty never expires
}

// CreateToken creates a scoped token in <OVDB home>/auth.json through the
// local server with the instance secret, starting it unless noStart
// (local-server-and-web-console#REQ:tokens-against-local-server). The body
// holds the token secret: the only time it exists outside the app using it.
func (l *Local) CreateToken(ctx context.Context, request TokenRequest, noStart bool) ([]byte, error) {
	return l.tokens(ctx, DataRequest{Method: http.MethodPost, URLPath: TokensPath, Body: request}, "create", noStart)
}

// Tokens lists the tokens, without their secrets.
func (l *Local) Tokens(ctx context.Context, noStart bool) ([]byte, error) {
	return l.tokens(ctx, DataRequest{Method: http.MethodGet, URLPath: TokensPath}, "list", noStart)
}

// RevokeToken revokes the token with id.
func (l *Local) RevokeToken(ctx context.Context, id string, noStart bool) ([]byte, error) {
	return l.tokens(ctx, DataRequest{Method: http.MethodDelete, URLPath: TokensPath + "/" + url.PathEscape(id)}, "revoke", noStart)
}

func (l *Local) tokens(ctx context.Context, request DataRequest, verb string, noStart bool) ([]byte, error) {
	status, body, err := l.v1(ctx, request, noStart)
	if err != nil || status < http.StatusMultipleChoices {
		return body, err
	}
	return nil, MapTokens(status, body, verb)
}

// MapTokens maps a /v1/tokens failure to the envelope people see; with
// --json the /v1 body is printed unchanged.
func MapTokens(status int, body []byte, verb string) *V1Error {
	var parsed v1ErrorBody
	_ = json.Unmarshal(body, &parsed)
	code, known := v1Codes[strings.ToLower(parsed.Error.Code)]
	if !known {
		code = envelope.Internal
	}
	message := uicopy.T("token.failed.create", nil)
	switch verb {
	case "list":
		message = uicopy.T("token.failed.list", nil)
	case "revoke":
		message = uicopy.T("token.failed.revoke", nil)
	}
	e := envelope.New(code, message)
	if parsed.Error.Message != "" {
		e = e.WithReason(redact.String(parsed.Error.Message))
	}
	switch code {
	case envelope.NotFound:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.token_list", nil), Command: "ovdb token list"})
	case envelope.InvalidArgument:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.help", nil), Command: "ovdb token " + verb + " --help"})
	default:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.server_status", nil), Command: "ovdb server status"})
	}
	return &V1Error{Status: status, Body: body, Envelope: e}
}
