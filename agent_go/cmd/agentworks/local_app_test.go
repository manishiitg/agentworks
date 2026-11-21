package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// #3157: use this Mac only when no network was chosen. A custom login, failed
// selected account, explicit server or environment credential never falls back.
func TestLocalBootstrapDoesNotOverrideSelectedLogin(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-home-") // short: socket paths have a ~104 byte limit
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	for _, key := range []string{"AGENTWORKS_CONFIG", "AGENTWORKS_SERVER", "AGENTWORKS_TOKEN"} {
		t.Setenv(key, "")
	}
	socket, err := agentworksclient.LocalAppSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	path, err := agentworksclient.LocalAppConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	o := &options{getenv: os.Getenv}
	cfg, got, err := o.connection(false)
	if err != nil || got != path || cfg.Server != agentworksclient.LocalAppServer {
		t.Fatal("local fallback missing", err)
	}
	custom := &options{getenv: os.Getenv, configPath: filepath.Join(root, "absent-login.json")}
	if _, _, err := custom.connection(false); err == nil {
		t.Fatal("explicit missing login switched to the Mac")
	}
	saved, err := agentworksclient.DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	remote := agentworksclient.Config{Server: "https://example.com", Token: "fixture-remote"}
	if err := agentworksclient.SaveConfig(saved, remote); err != nil {
		t.Fatal(err)
	}
	cfg, got, err = o.connection(false)
	if err != nil || got != saved || cfg.Server != remote.Server || cfg.Token != remote.Token {
		t.Fatal("selected login was replaced", err)
	}
}
