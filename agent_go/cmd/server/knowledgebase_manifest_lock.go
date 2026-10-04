package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"golang.org/x/sys/unix"
	"os"
)

// Serialize the CAS check and rename with ordinary server manifest updates.
func knowledgeManifestLock(path string) (func(), error) {
	f, err := os.OpenFile(path+".kb-lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "Project configuration is being updated; retry.", Retryable: true}
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
