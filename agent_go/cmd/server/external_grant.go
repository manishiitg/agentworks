package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

const externalBuilderErrorLimit = 64 << 10

// Resolve both token kinds without ever converting a restricted connection
// into an unrestricted app identity (including when restoring delayed turns).
func activeExternalGrantClaims(ctx context.Context, id string) (*UserClaims, error) {
	if strings.HasPrefix(id, "oauth-") {
		g, err := mcpOAuthFamilyActive(ctx, strings.TrimPrefix(id, "oauth-"))
		if err != nil {
			return nil, err
		}
		return accessTokenClaims(mcpOAuthTokenForGrant(g))
	}
	s, err := openAccessTokens()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	token, err := s.Active(ctx, id, time.Now())
	if err != nil {
		return nil, err
	}
	return accessTokenClaims(token)
}

func validateBuilderGrant(ctx context.Context, claims *UserClaims, workflow, workspace string) (*UserClaims, error) {
	if claims == nil || claims.AccessToken == nil {
		return nil, errors.New("Builder MCP is not authorized")
	}
	live, err := activeExternalGrantClaims(ctx, claims.AccessToken.ID)
	if err != nil {
		return nil, err
	}
	if live.UserID != claims.UserID || !live.AccessToken.AllowsWorkflow(workflow) || !userAllowedWorkflowID(live, workflow) {
		return nil, errors.New("Builder grant no longer authorizes this workflow")
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, live, workspace)
	if manifest == nil || manifest.ID != workflow || (level != WorkflowAccessOwner && level != WorkflowAccessWrite) {
		return nil, errors.New("workflow write access is required")
	}
	product := "agentworks"
	authorized := live.AccessToken.BuilderAccess()
	if manifest.Kind == "relay" {
		product = "relays"
		authorized = authorized || live.AccessToken.RelayBuilderAccess()
	}
	if !authorized || !userAllowedProduct(live, product) {
		return nil, errors.New("Builder grant does not authorize this product")
	}
	return live, nil
}

func externalBuilderForward(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	forward := &externalBuilderResponse{target: w, header: make(http.Header)}
	handler(forward, r)
	if forward.status == 0 {
		forward.WriteHeader(http.StatusOK)
	}
	if forward.status < 400 {
		return
	}
	code := "builder_failed"
	switch forward.status {
	case http.StatusBadRequest:
		code = "invalid_arguments"
	case http.StatusUnauthorized:
		code = "unauthorized"
	case http.StatusForbidden:
		code = "forbidden"
	case http.StatusNotFound:
		code = "session_not_found"
	case http.StatusConflict:
		code = "builder_conflict"
	case http.StatusTooManyRequests:
		code = "rate_limited"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		code = "builder_unavailable"
	}
	message := strings.TrimSpace(forward.body.String())
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(forward.body.Bytes(), &payload) == nil {
		var nested struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		var errorText string
		if json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "" {
			message = nested.Message
			if externalBuilderErrorCode(nested.Code) {
				code = nested.Code
			}
		} else if json.Unmarshal(payload.Error, &errorText) == nil && errorText != "" {
			message = errorText
			if externalBuilderErrorCode(errorText) {
				code = errorText
			}
		}
		if payload.Message != "" {
			message = payload.Message
		}
	}
	if message == "" {
		message = http.StatusText(forward.status)
	}
	if forward.truncated {
		message += " (error message truncated)"
	}
	externalError(w, forward.status, code, message)
}

// Successful query responses pass straight through; only errors are buffered,
// with a fixed cap. This preserves the runtime's actual HTTP status while
// normalizing both its plain-text and JSON errors to the external API contract.
type externalBuilderResponse struct {
	target    http.ResponseWriter
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (w *externalBuilderResponse) Header() http.Header { return w.header }

func (w *externalBuilderResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status >= 400 {
		return
	}
	for key, values := range w.header {
		w.target.Header()[key] = append([]string(nil), values...)
	}
	w.target.WriteHeader(status)
}

func (w *externalBuilderResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status < 400 {
		return w.target.Write(data)
	}
	n := len(data)
	remaining := externalBuilderErrorLimit - w.body.Len()
	if len(data) > remaining {
		data = data[:remaining]
		w.truncated = true
	}
	_, _ = w.body.Write(data)
	return n, nil
}

func (w *externalBuilderResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status < 400 {
		if flusher, ok := w.target.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func externalBuilderErrorCode(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, char := range s {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func externalBuilderInt(args map[string]interface{}, key string, fallback, min, max int) (int, error) {
	v, exists := args[key]
	if !exists {
		return fallback, nil
	}
	var n float64
	switch value := v.(type) {
	case float64:
		n = value
	case int:
		n = float64(value)
	case json.Number:
		var err error
		n, err = value.Float64()
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < float64(min) || n >= float64(max)+1 {
		return 0, fmt.Errorf("%s is outside the permitted range", key)
	}
	return int(n), nil
}

func externalBuilderString(args map[string]interface{}, key string) (string, error) {
	v, exists := args[key]
	if !exists {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(s), nil
}
