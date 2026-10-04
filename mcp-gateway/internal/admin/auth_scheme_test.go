package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestConnectorCredentialSchemeValidation(t *testing.T) {
	for _, tc := range []struct{ token, scheme string }{
		{"dXNlcjpwYXNz", "Basic"}, {"fixture", "Bearer"}, {"", ""},
	} {
		if err := validateConnectorCredential(tc.token, tc.scheme); err != nil {
			t.Fatal("valid credential rejected", err)
		}
	}
	for _, tc := range []struct{ token, scheme string }{
		{"fixture", "Other"}, {"invalid", "Basic"}, {"bm9jb2xvbg==", "Basic"}, {"value\r\nInjected", "Bearer"},
	} {
		if err := validateConnectorCredential(tc.token, tc.scheme); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}

func TestCustomBasicConnectorPersistsAndRotatesWithoutLeaking(t *testing.T) {
	origin := server.NewMCPServer("basic-fixture", "1")
	origin.AddTool(mcp.NewTool("read"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	handler := server.NewStreamableHTTPServer(origin, server.WithStateLess(true))
	want := "Basic dXNlcjpwYXNz"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	st, err := store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.AddWorkspace(store.Workspace{ID: "w"})
	g := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	a := &Admin{Store: st, Gateway: g, WorkspaceID: "w"}
	c, err := a.AddConnectorCustomWithCredential(context.Background(), "langfuse", "Langfuse", "", remote.URL+"/mcp", "dXNlcjpwYXNz", "Basic")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), "dXNlcjpwYXNz") {
		t.Fatal("API exposes credential")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, ok := st.GetConnector(c.ID)
	if !ok || c.AuthScheme != "Basic" || st.ConnectorBearer(c.ID) != "dXNlcjpwYXNz" {
		t.Fatal("Basic credential not restored")
	}
	// A real discovery during rotation must also send the preserved scheme.
	want = "Basic dXNlcjpuZXc="
	g = mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	if err := g.ReplaceConnectorCredentials(context.Background(), c, "dXNlcjpuZXc="); err != nil {
		t.Fatal(err)
	}
	if st.ConnectorBearer(c.ID) != "dXNlcjpuZXc=" {
		t.Fatal("rotated credential not stored")
	}
}
