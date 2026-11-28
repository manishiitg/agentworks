package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// TerminalSession is deliberately separate from tool-result JSON: the one
// journal may exceed the ordinary tool response budget and is streamed.
func (c *Client) TerminalSession(ctx context.Context, key, action string) (io.ReadCloser, error) {
	q := url.Values{"conversation_key": {key}, "action": {action}}
	method := http.MethodPost
	if action == "command" {
		method = http.MethodGet
	}
	req, e := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/external/v1/chats/terminal?"+q.Encode(), nil)
	if e != nil {
		return nil, e
	}
	token, e := c.authToken(ctx)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := *c.http
	client.Timeout = 15 * time.Minute
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Body, nil
	}
	defer resp.Body.Close()
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out)
	if out.Error.Message == "" {
		out.Error.Message = "The chat could not be copied to your terminal. Try again from its menu in AgentWorks."
	}
	return nil, &APIError{Status: resp.StatusCode, Code: out.Error.Code, Message: out.Error.Message}
}

func (c *Client) TerminalCommand(ctx context.Context, key string) (map[string]any, error) {
	body, e := c.TerminalSession(ctx, key, "command")
	if e != nil {
		return nil, e
	}
	defer body.Close()
	var out map[string]any
	e = json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&out)
	if e != nil {
		return nil, fmt.Errorf("invalid terminal command reply")
	}
	return out, nil
}
