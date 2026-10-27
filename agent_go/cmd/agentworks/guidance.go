package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Guidance is checked for server tool commands, never local help, login,
// installation or executor startup. Failure cannot prevent the requested call.
// stdout remains JSON/MCP-only and the cache contains no text or credentials.
func (o *options) checkGuidanceAtStart(cmd commandContext) {
	path := strings.Fields(cmd.CommandPath())
	if len(path) < 2 {
		return
	}
	switch path[1] {
	case "tools", "files", "runs", "schedules", "chat", "messages", "guidance", "knowledge", "functions", "crews", "plan", "mcp":
	default:
		return
	}
	// The explicit context command itself performs this read.
	if len(path) > 2 && path[1] == "guidance" && path[2] == "context" {
		return
	}
	client, err := o.client()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
	defer cancel()
	raw, err := client.Call(ctx, "help", map[string]any{"action": "context"})
	if err != nil {
		return
	}
	var current struct {
		Version string `json:"guidance_version"`
	}
	if json.Unmarshal(raw, &current) != nil || current.Version == "" {
		return
	}
	cfg, configPath, err := o.connection(false)
	if err != nil {
		return
	}
	cachePath := configPath + ".guidance.json"
	var previous struct {
		Server  string `json:"server"`
		Version string `json:"guidance_version"`
	}
	if info, err := os.Lstat(cachePath); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 4096 {
			return
		}
		if saved, err := os.ReadFile(cachePath); err == nil {
			_ = json.Unmarshal(saved, &previous)
		}
	}
	if previous.Server == cfg.Server && previous.Version != "" && previous.Version != current.Version {
		fmt.Fprintln(o.stderr, "AgentWorks guidance changed. Refresh selected topics with agentworks guidance topics/topic or help action=skills; cached topic text may be stale.")
	}
	previous.Server, previous.Version = cfg.Server, current.Version
	data, err := json.Marshal(previous)
	if err != nil || os.MkdirAll(filepath.Dir(cachePath), 0700) != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(cachePath), ".guidance-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return
	}
	if f.Close() == nil {
		_ = os.Rename(f.Name(), cachePath)
	}
}

type commandContext interface {
	CommandPath() string
	Context() context.Context
}
