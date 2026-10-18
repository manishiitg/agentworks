package services

import (
	"strings"
	"sync/atomic"
)

// The server supplies the existing trusted project-alias fold. Standalone
// service users keep exact paths; this hook never grants access or rewrites
// credentials, account ownership, or stored selections.
var connectionWorkspaceResolver atomic.Value

func SetConnectionWorkspaceResolver(resolve func(string) string) {
	if resolve != nil {
		connectionWorkspaceResolver.Store(resolve)
	}
}

func connectionWorkspaceRoot(root string) string {
	root = strings.Trim(strings.TrimSpace(root), "/")
	if resolve, ok := connectionWorkspaceResolver.Load().(func(string) string); ok {
		root = resolve(root)
	}
	return strings.Trim(root, "/")
}
