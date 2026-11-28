package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #4576: handleAccountModelPicker serves stale cached CLI models immediately
// while refreshing in the background, and surfaces the updated list once the
// background refresh completes.
func TestAccountModelPickerSWRServesStaleAndRefreshesInBackground(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	acct := env.addAccount(t, "alice", map[string]interface{}{
		"provider":     "cursor-cli",
		"display_name": "Test Cursor",
		"credential":   "test-swr-key",
	})

	bin := t.TempDir()
	marker := filepath.Join(bin, "v2")
	script := `#!/bin/sh
if [ -f "` + marker + `" ]; then
  sleep 0.15
  printf 'model-v2 - Model V2\n'
else
  printf 'model-v1 - Model V1\n'
fi
`
	if err := os.WriteFile(filepath.Join(bin, "cursor-agent"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CURSOR_CLI_TRANSPORT", "acp")

	oldFresh, oldStale := accountModelsFresh, accountModelsStale
	accountModelsFresh = 50 * time.Millisecond
	accountModelsStale = 10 * time.Second
	t.Cleanup(func() {
		accountModelsFresh = oldFresh
		accountModelsStale = oldStale
		accountModelCatalog.Lock()
		accountModelCatalog.models = make(map[string][]dynamicModelEntry)
		accountModelCatalog.fetched = make(map[string]time.Time)
		accountModelCatalog.refreshing = make(map[string]bool)
		accountModelCatalog.Unlock()
	})

	fetch := func() (dynamicModelsResponse, time.Duration) {
		t.Helper()
		start := time.Now()
		w := env.do(t, env.api.handleGetProviderModels, http.MethodGet, "/models?latest=true&connection_id="+acct.ID, "alice", nil, map[string]string{"provider": "cursor-cli"})
		elapsed := time.Since(start)
		if w.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		var resp dynamicModelsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp, elapsed
	}

	// 1. Cold call fetches v1.
	resp1, _ := fetch()
	if len(resp1.Models) != 1 || resp1.Models[0].ModelID != "model-v1" {
		t.Fatalf("cold models = %+v, want [model-v1]", resp1.Models)
	}

	// 2. Flip CLI to slow v2 and wait past fresh TTL (still inside stale window).
	if err := os.WriteFile(marker, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)

	// 3. Stale hit returns v1 immediately (<80ms) while refreshing in background.
	resp2, elapsed := fetch()
	if elapsed > 80*time.Millisecond {
		t.Fatalf("SWR call took %v, want fast stale return (<80ms)", elapsed)
	}
	if len(resp2.Models) != 1 || resp2.Models[0].ModelID != "model-v1" {
		t.Fatalf("stale models = %+v, want [model-v1]", resp2.Models)
	}

	// 4. After background refresh finishes, next call serves fresh v2.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp3, _ := fetch()
		if len(resp3.Models) == 1 && resp3.Models[0].ModelID == "model-v2" {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("background SWR refresh did not update cached models to model-v2")
}
