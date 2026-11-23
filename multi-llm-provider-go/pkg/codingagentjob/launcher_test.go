package codingagentjob

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	helperProcessEnv = "LLM_PROVIDER_MCP_TEST_HELPER_PROCESS"
	helperMarkerEnv  = "LLM_PROVIDER_MCP_TEST_HELPER_MARKER"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperProcessEnv) == "1" {
		marker := os.Getenv(helperMarkerEnv)
		payload := strings.Join(os.Args[1:], " ") + "\n" + os.Getenv(EnvDelegationDepth)
		if err := os.WriteFile(marker, []byte(payload), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestProcessLauncherStartsDetachedWorker(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "worker-started")
	t.Setenv(helperProcessEnv, "1")
	t.Setenv(helperMarkerEnv, marker)
	launcher := ProcessLauncher{
		Executable: os.Args[0],
		StatePath:  filepath.Join(dir, "jobs.db"),
		LogDir:     filepath.Join(dir, "logs"),
	}
	pid, err := launcher.Launch("job_launcher_test", 1)
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if pid <= 0 {
		t.Fatalf("Launch() PID = %d", pid)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		data, readErr := os.ReadFile(marker)
		if readErr == nil {
			payload := string(data)
			if !strings.Contains(payload, "worker --job-id job_launcher_test --state") {
				t.Fatalf("worker args = %q", payload)
			}
			if !strings.HasSuffix(payload, "\n1") {
				t.Fatalf("delegation depth payload = %q", payload)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker marker was not created: %v", readErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
