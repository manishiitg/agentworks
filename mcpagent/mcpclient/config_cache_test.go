package mcpclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type configReadLogger struct {
	loggerv2.Logger
	reads int
}

func (l *configReadLogger) Debug(message string, fields ...loggerv2.Field) {
	if message == "About to read config file" {
		l.reads++
	}
}

// PLAT-746-876: unchanged catalogs were reread/decoded repeatedly per build.
// Cached config must never leak a caller's credentials or runtime mutations.
func TestMergedConfigReadsOnceUntilEitherFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	user := strings.Replace(path, ".json", "_user.json", 1)
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(path, `{"mcpServers":{"example":{"command":"base","args":["original"],"headers":{"Authorization":"example-only"},"oauth":{"scopes":["read"],"extra_auth_params":{"prompt":"consent"}}}}}`)
	logger := &configReadLogger{Logger: loggerv2.NewNoop()}
	load := func() *MCPConfig {
		t.Helper()
		cfg, err := LoadMergedConfig(path, logger)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	for i := 0; i < 20; i++ {
		cfg := load()
		server := cfg.MCPServers["example"]
		if server.Command != "base" || server.Args[0] != "original" || server.Headers["Authorization"] != "example-only" || server.OAuth.Scopes[0] != "read" || server.OAuth.ExtraAuthParams["prompt"] != "consent" {
			t.Fatal("caller mutated cached config")
		}
		server.Args[0] = "changed"
		server.Headers["Authorization"] = "changed"
		server.OAuth.Scopes[0] = "changed"
		server.OAuth.ExtraAuthParams["prompt"] = "changed"
		delete(cfg.MCPServers, "example")
	}
	if logger.reads != 2 {
		t.Fatalf("repeated reads: got%d want2 (base and absent user)", logger.reads)
	}
	write(user, `{"mcpServers":{"added":{"command":"user"}}}`)
	if load().MCPServers["added"].Command != "user" {
		t.Fatal("user creation not observed")
	}
	// Same-size base edit, changed mtime; size alone is insufficient.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write(path, strings.Replace(string(raw), `"base"`, `"next"`, 1))
	stamp := time.Now().Add(time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if load().MCPServers["example"].Command != "next" {
		t.Fatal("base change not observed")
	}
	if err := os.Remove(user); err != nil {
		t.Fatal(err)
	}
	if _, ok := load().MCPServers["added"]; ok {
		t.Fatal("removed user catalog retained")
	}
	if logger.reads != 8 {
		t.Fatalf("expected two reads per changed pair, got%d", logger.reads)
	}
}
