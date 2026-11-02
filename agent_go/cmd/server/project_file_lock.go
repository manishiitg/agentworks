package server

import (
	"os"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filelock"
)

// Keep the existing cross-process blocking semantics on both Unix and Windows.
func lockProjectFile(file *os.File) error {
	for {
		locked, err := filelock.TryLock(file)
		if err != nil || locked {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
