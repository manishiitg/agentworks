package mcpclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestSaveConfigConcurrentSavesLeaveAValidFile: concurrent saves once shared
// one ".tmp" path, so one save could rename another's half-written file into
// place and leave a broken config.
func TestSaveConfigConcurrentSavesLeaveAValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp_servers.json")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := &MCPConfig{MCPServers: map[string]MCPServerConfig{}}
			for j := 0; j <= i*20; j++ {
				cfg.MCPServers[fmt.Sprintf("server-%d", j)] = MCPServerConfig{URL: "https://example.com/mcp"}
			}
			if err := SaveConfig(path, cfg); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got MCPConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config left unreadable after concurrent saves: %v", err)
	}
	leftovers, _ := filepath.Glob(path + ".*.tmp")
	if len(leftovers) > 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}
