package agycli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

var agyPrivateHomesSweepOnce sync.Once

func agyPIDDead(pid int) bool {
	if pid <= 0 || pid == os.Getpid() {
		return false
	}
	return !procshutdown.Alive(pid)
}

// Private homes from a killed backend hold only that backend's MCP config,
// but they still contain credentials until removed. Reclaim only homes whose
// encoded PID is gone; a second live backend keeps its homes intact.
func agySweepStalePrivateHomes() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agentworks-agy-") {
			continue
		}
		rest := strings.TrimPrefix(entry.Name(), "agentworks-agy-")
		pidText, suffix, ok := strings.Cut(rest, "-")
		pid, err := strconv.Atoi(pidText)
		if !ok || err != nil || len(suffix) < 6 || !agyPIDDead(pid) {
			continue
		}
		path := filepath.Join(os.TempDir(), entry.Name())
		info, err := os.Lstat(path)
		if err == nil && info.IsDir() && info.Mode().Perm() == 0o700 {
			_ = os.RemoveAll(path)
		}
	}
}
