package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestDiscoveryContactsOnlyRequestedServer(t *testing.T) {
	var selectedCalls, otherCalls atomic.Int32
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selectedCalls.Add(1)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(405)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "test", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "ping", "description": "test", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer selected.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { otherCalls.Add(1); w.WriteHeader(500) }))
	defer other.Close()
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"selected": {URL: selected.URL, Protocol: mcpclient.ProtocolHTTP},
		"unused":   {URL: other.URL, Protocol: mcpclient.ProtocolHTTP},
	}}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{logger: loggerv2.NewNoop(), mcpConfig: cfg, mcpConfigPath: path, serverLogs: map[string][]ServerLogEntry{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := api.discoverServerToolsDetailed(ctx, "selected")
	if err != nil || status.Status != "ok" {
		t.Fatalf("discovery: %+v %v", status, err)
	}
	if selectedCalls.Load() == 0 || otherCalls.Load() != 0 {
		t.Fatalf("selected=%d unused=%d", selectedCalls.Load(), otherCalls.Load())
	}
	before := selectedCalls.Load()
	_, err = api.discoverServerToolsDetailed(ctx, "selected")
	if err != nil {
		t.Fatal(err)
	}
	if selectedCalls.Load() != before {
		t.Fatal("cached discovery reconnected")
	}
}

func TestCatalogOperationsNeverConnect(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unused server must not be contacted", 500)
	}))
	defer remote.Close()
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"unused-a": {URL: remote.URL},
		"unused-b": {URL: remote.URL + "/b"},
	}}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{logger: loggerv2.NewNoop(), mcpConfig: cfg, mcpConfigPath: path, toolStatus: map[string]ToolStatus{}, serverLogs: map[string][]ServerLogEntry{}}
	api.initializeToolCache()
	api.triggerMCPDiscovery()
	api.invalidateServerDiscovery("unused-a", "Disconnected")
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		api.handleGetTools(w, httptest.NewRequest("GET", "/api/tools", nil).WithContext(personContext("alice")))
		var statuses []ToolStatus
		if err := json.Unmarshal(w.Body.Bytes(), &statuses); err != nil {
			t.Fatal(err)
		}
		if len(statuses) != 2 {
			t.Fatalf("statuses: %s", w.Body.String())
		}
		for _, status := range statuses {
			if status.Status != "not_connected" {
				t.Fatalf("unexpected status: %+v", status)
			}
		}
	}
	if api.discoveryRunning || api.discoveryTicker != nil {
		t.Fatal("catalog started background discovery")
	}
	if calls.Load() != 0 {
		t.Fatalf("unused servers contacted %d times", calls.Load())
	}
}
