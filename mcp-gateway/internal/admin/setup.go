package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/setupagent"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func (a *Admin) setupTool(ctx context.Context, name string, raw json.RawMessage, actors ...string) (any, error) {
	switch name {
	case "connect_server":
		var in struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			Instance string `json:"instance,omitempty"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			return nil, err
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return nil, errors.New("expected one connection object")
		}
		in.Name, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL)
		if in.Name == "" || len(in.Name) > 100 || in.URL == "" || len(in.URL) > 2048 || len(in.Instance) > 100 {
			return nil, errors.New("provide a server name and MCP URL")
		}
		// Use the same URL, private-network, uniqueness and discovery checks as
		// manual connections. Credentials never pass through the model tool.
		c, err := a.AddConnectorCustom(ctx, in.Name, in.Name, in.Instance, in.URL)
		if err != nil {
			return nil, err
		}
		return map[string]any{"connector": c, "tool_count": len(a.Store.ListToolsForConnector(c.ID)), "group_access_assigned": false}, nil
	case "inspect_environment":
		tools := a.Store.ListTools(a.WorkspaceID)
		brief := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			brief = append(brief, map[string]any{"public_name": t.PublicName, "connector_id": t.ConnectorID, "status": t.Status, "fingerprint": t.Fingerprint, "description": t.Description})
		}
		return map[string]any{"groups": a.Store.ListGroups(a.WorkspaceID), "connectors": a.Store.ListConnectors(a.WorkspaceID), "tools": brief, "packages": a.Store.ListPackages(a.WorkspaceID)}, nil
	case "inspect_tool":
		var in struct {
			PublicName string `json:"public_name"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		t, ok := a.Store.GetTool(in.PublicName)
		if !ok || t.WorkspaceID != a.WorkspaceID {
			return nil, errors.New("tool not found")
		}
		return map[string]any{"public_name": t.PublicName, "description": t.Description, "status": t.Status, "fingerprint": t.Fingerprint, "input_schema": json.RawMessage(t.InputSchema)}, nil
	case "save_draft":
		var in access.Package
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if in.ID == "" {
			in.ID = newID("ap")
		}
		in.WorkspaceID = a.WorkspaceID
		if err := a.validateAccessPackage(in); err != nil {
			return nil, err
		}
		saved, ok := a.Store.SavePackageDraft(in, in.Version)
		if !ok {
			return nil, errors.New("package version changed; inspect the current draft")
		}
		a.Store.AppendPolicyEvent(a.WorkspaceID, store.PolicyEvent{At: time.Now().UTC(), Actor: setupActor(actors), Action: "save_draft", PackageID: saved.ID, Version: saved.Version})
		return saved, nil
	default:
		return nil, errors.New("unknown setup tool")
	}
}

func (a *Admin) setupRoutes(mux *http.ServeMux) {
	// The shared product chat invokes deterministic connection/draft operations.
	// The service credential is checked before any operation; this endpoint
	// has no publish, membership, upstream execution or credential operation.
	mux.HandleFunc("/api/admin/setup/tool", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Operation string          `json:"operation"`
			Arguments json.RawMessage `json:"arguments"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			writeErr(w, http.StatusBadRequest, errors.New("expected one JSON object"))
			return
		}
		if !json.Valid(in.Arguments) || len(in.Arguments) == 0 || in.Arguments[0] != '{' {
			writeErr(w, http.StatusBadRequest, errors.New("arguments must be an object"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		result, err := a.setupTool(ctx, in.Operation, in.Arguments, adminActor(r))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))

	mux.HandleFunc("/api/admin/setup/status", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":    a.SetupAgent != nil && a.SetupAgent.Configured(),
			"default_model": a.SetupAgent.DefaultModel(),
			"models":        a.SetupAgent.Models(),
		})
	}))
	mux.HandleFunc("/api/admin/setup/chat", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if a.SetupAgent == nil || !a.SetupAgent.Configured() {
			writeErr(w, http.StatusServiceUnavailable, errors.New("set CAPLAYER_AGENT_API_URL and CAPLAYER_AGENT_MODEL or CAPLAYER_AGENT_MODELS to enable setup chat"))
			return
		}
		var in struct {
			Messages []setupagent.Message `json:"messages"`
			Model    string               `json:"model"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, err := a.SetupAgent.SelectModel(in.Model); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		reply, err := a.SetupAgent.Run(ctx, in.Model, in.Messages, func(name string, raw json.RawMessage) (any, error) { return a.setupTool(ctx, name, raw, adminActor(r)) })
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"reply": reply, "at": time.Now().UTC().Format(time.RFC3339)})
	}))
}

func setupActor(actors []string) string {
	if len(actors) > 0 && actors[0] != "" {
		return actors[0]
	}
	return "setup-agent"
}
