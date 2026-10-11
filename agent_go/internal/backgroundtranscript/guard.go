// Package backgroundtranscript coordinates transcript writers with chat deletion.
package backgroundtranscript

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

type sessionGuard struct {
	sync.Mutex
	deleted bool
}

var sessions sync.Map

func guard(workspace, session string) *sessionGuard {
	key := filepath.Join(fsutil.WorkspaceDocsRoot(), strings.Trim(workspace, "/"), "builder/conversation/background", session)
	value, _ := sessions.LoadOrStore(key, &sessionGuard{})
	return value.(*sessionGuard)
}

// Lock serializes all writers for one session, including the server's lifecycle
// writer and the orchestrator's event writer. Deleted sessions stay live-only.
func Lock(workspace, session string) (unlock func(), deleted bool) {
	item := guard(workspace, session)
	item.Lock()
	return item.Unlock, item.deleted
}

// Delete removes files while holding the same lock, then fences late writes for
// this process's lifetime. After restart event appends still require a header;
// old lifecycle writers no longer have an active session to resolve a workspace.
func Delete(workspace, session string, remove func() error) error {
	item := guard(workspace, session)
	item.Lock()
	defer item.Unlock()
	if err := remove(); err != nil {
		return err
	}
	item.deleted = true
	return nil
}
