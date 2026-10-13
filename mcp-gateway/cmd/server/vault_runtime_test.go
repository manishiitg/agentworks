package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/pkg/vaultruntime"
)

func TestPrivateVaultRuntimeSelectionFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("GATEWAY_STATE_DIR", root)
	for _, mode := range []string{"private", "unknown"} {
		if err := runVaultRuntime(mode, nil); err == nil {
			t.Fatalf("accepted %s without an installed runtime", mode)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("failed selector touched state: %v", err)
		}
	}
	bad := &vaultruntime.Factory{Version: vaultruntime.Version + 1, Run: func([]api.Factory) error { t.Fatal("called incompatible runtime"); return nil }}
	if err := runVaultRuntime("private", bad); err == nil {
		t.Fatal("accepted incompatible runtime")
	}
	want := errors.New("private startup failed")
	called := 0
	private := &vaultruntime.Factory{Version: vaultruntime.Version, Run: func(products []api.Factory) error {
		called++
		if len(products) != 0 {
			t.Fatal("public build supplied private products")
		}
		return want
	}}
	if err := runVaultRuntime(" private ", private); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("private was retried or bypassed")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("fell back into public startup")
	}
}
