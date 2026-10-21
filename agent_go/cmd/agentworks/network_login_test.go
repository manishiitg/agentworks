package main

import (
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestPublicNetworkLoginDefaultPreservesSavedServer(t *testing.T) {
	o := &options{configPath: filepath.Join(t.TempDir(), "config.json"), getenv: func(string) string { return "" }}
	first, _, err := o.connection(true)
	if err != nil || first.Server != "https://network.agentworkshq.com" || first.Token != "" {
		t.Fatal("first sign-in must offer the public network")
	}
	if err := agentworksclient.SaveConfig(o.configPath, agentworksclient.Config{Server: "https://company.example.com", Token: "private-fixture"}); err != nil {
		t.Fatal(err)
	}
	saved, _, err := o.connection(true)
	if err != nil || saved.Server != "https://company.example.com" {
		t.Fatal("saved company sign-in was replaced by the default")
	}
	o.serverURL = "https://network.agentworkshq.com"
	explicit, _, err := o.connection(true)
	if err != nil || explicit.Token != "" || explicit.RefreshToken != "" {
		t.Fatal("a server change carried another server's credentials")
	}
}
