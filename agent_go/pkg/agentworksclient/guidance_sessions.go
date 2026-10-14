package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type guidanceContextKey struct{}
type guidanceSession struct {
	sync.Mutex
	id   string
	save func(string)
}

// WithGuidanceSession selects one explicitly identified conversation. A nil save
// callback never silently starts account-wide or transport-wide deduplication.
func (c *Client) WithGuidanceSession(id string, save func(string)) *Client {
	c.guidance.Lock()
	defer c.guidance.Unlock()
	c.guidance.id, c.guidance.save = id, save
	return c
}
func WithGuidanceSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, guidanceContextKey{}, id)
}
func (c *Client) guidanceID(ctx context.Context) string {
	if id, ok := ctx.Value(guidanceContextKey{}).(string); ok {
		return id
	}
	c.guidance.Lock()
	defer c.guidance.Unlock()
	return c.guidance.id
}
func (c *Client) rememberGuidance(data json.RawMessage, usedHandle ...string) {
	var value struct {
		Guidance json.RawMessage `json:"guidance"`
	}
	if json.Unmarshal(data, &value) != nil || len(value.Guidance) == 0 {
		return
	}
	var receipt struct {
		ID string `json:"guidance_session_id"`
	}
	_ = json.Unmarshal(value.Guidance, &receipt)
	var compact string
	_ = json.Unmarshal(value.Guidance, &compact)
	c.guidance.Lock()
	save := c.guidance.save
	id := receipt.ID
	// Refresh the native cache's activity timestamp without echoing a long ID
	// in every response. A generic client still owns its explicit handle.
	if id == "" && len(usedHandle) > 0 && usedHandle[0] == c.guidance.id && strings.HasSuffix(compact, " current") {
		id = c.guidance.id
	}
	if save != nil && id != "" {
		c.guidance.id = id
	}
	c.guidance.Unlock()
	if save != nil && id != "" {
		save(id)
	}
}

// Schemas is explicit preparation: returned skill bodies must be shown to the
// agent. Tool listing and background version checks do not establish delivery.
func (c *Client) Schemas(ctx context.Context, names []string) (json.RawMessage, error) {
	query := url.Values{}
	for _, name := range names {
		query.Add("names", name)
	}
	if id := c.guidanceID(ctx); id != "" {
		query.Set("guidance_session_id", id)
	}
	raw, err := c.request(ctx, http.MethodGet, "/api/external/v1/tools?"+query.Encode(), nil, true)
	if err == nil {
		c.rememberGuidance(raw)
	}
	return raw, err
}
