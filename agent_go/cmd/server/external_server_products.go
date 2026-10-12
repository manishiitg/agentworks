package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const serverProductManagementTool = "server_product"

func serverProductManagerAllowed(c *UserClaims, product string) bool {
	if c == nil || c.Scope != "" || c.Provider == "bot_route" || !activeMCPPerson(c.UserID) || !claimsIsAdmin(c) {
		return false
	}
	if c.AccessToken != nil && (c.AccessToken.BoundAgentID != "" || !c.AccessToken.Allows("users:manage")) {
		return false
	}
	return registeredServerProducts[product] && productEnabled(product) && userAllowedProduct(c, product)
}

func serverProductManagementAllowed(c *UserClaims) bool {
	for product := range registeredServerProducts {
		if serverProductManagerAllowed(c, product) {
			return true
		}
	}
	return false
}

func externalServerProductDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	products := []string{}
	for product := range registeredServerProducts {
		products = append(products, product)
	}
	sort.Strings(products)
	values := make([]any, len(products))
	for i, product := range products {
		values[i] = product
	}
	add(serverProductManagementTool, "Manage an installed server product through its private tool catalog. Current administrators with users:manage consent only. First action=tools, then action=call with the returned tool name and arguments. No workflow ID or provider credentials are accepted.", true, false, map[string]any{
		"product":   map[string]any{"type": "string", "enum": values},
		"action":    map[string]any{"type": "string", "enum": []any{"tools", "call"}},
		"name":      externalString("Product tool name from action=tools; required for action=call."),
		"arguments": map[string]any{"type": "object", "description": "Arguments matching the private product tool schema."},
	}, "product", "action")
}

// A fixed management hop, not an arbitrary HTTP proxy. This authority is
// separate from inference PATs and the read-only product browser cookie.
func (api *StreamingAPI) externalServerProductCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	c := GetUserFromContext(r.Context())
	product := externalArg(args, "product")
	if !serverProductManagerAllowed(c, product) {
		externalError(w, 403, "forbidden", "Current product administrator and users:manage consent required.")
		return
	}
	method, tail := http.MethodGet, "/management/tools"
	var body []byte
	switch externalArg(args, "action") {
	case "tools":
		if args["name"] != nil || args["arguments"] != nil {
			externalError(w, 400, "invalid_arguments", "tools accepts product and action only.")
			return
		}
	case "call":
		if externalArg(args, "name") == "" {
			externalError(w, 400, "invalid_arguments", "Product tool name required.")
			return
		}
		body, _ = json.Marshal(map[string]any{"name": args["name"], "arguments": args["arguments"]})
		if len(body) > 65536 {
			externalError(w, 400, "invalid_arguments", "Product arguments too large.")
			return
		}
		method, tail = http.MethodPost, "/management/call"
	default:
		externalError(w, 400, "invalid_arguments", "Unknown product action.")
		return
	}
	target, secret, err := serverProductServiceConfig(product)
	if err != nil {
		externalError(w, 503, "product_unavailable", "Server product unavailable.")
		return
	}
	// Bind existing platform accounts before a product inspects or changes access.
	users, err := vaultDirectoryUsers(c)
	if err != nil {
		externalError(w, 503, "directory_unavailable", "User directory unavailable.")
		return
	}
	for _, user := range users {
		if err := syncServerProductDirectoryUser(r.Context(), user.ID, user.Email); err != nil {
			externalError(w, 503, "directory_unavailable", "User directory synchronization unavailable.")
			return
		}
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/api/admin/products/" + product + tail
	req, err := http.NewRequestWithContext(r.Context(), method, target.String(), bytes.NewReader(body))
	if err != nil {
		externalError(w, 503, "product_unavailable", "Server product unavailable.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentWorks-Actor", c.UserID)
	req.Header.Set("X-AgentWorks-Admin", "1")
	clientID := "platform"
	if c.AccessToken != nil {
		clientID = "access-token:" + c.AccessToken.ID
	}
	req.Header.Set("X-AgentWorks-Client", clientID)
	client := &http.Client{Transport: capLayerTransport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		externalError(w, 502, "product_unavailable", "Server product unavailable.")
		return
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(payload) > 2<<20 || !json.Valid(payload) || response.StatusCode < 200 || response.StatusCode >= 300 {
		status := http.StatusBadGateway
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			status = response.StatusCode
		}
		externalError(w, status, "product_operation_failed", "Product operation failed; refresh the tool schema and current revision before retrying.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, json.RawMessage(payload))
}
