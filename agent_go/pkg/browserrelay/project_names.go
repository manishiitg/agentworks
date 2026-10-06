package browserrelay

import (
	"path"
	"sync"
	"time"
)

// Names are display metadata, never authority. The websocket reader only copies
// cached values; one worker resolves missing/expired names outside that loop.
// Authorization is still checked live by availableProjects on every heartbeat.
type projectNameKey struct{ scope, workspace, profile string }
type projectNameEntry struct {
	name    string
	pending bool
}
type projectNames struct {
	mu      sync.Mutex
	entries map[projectNameKey]*projectNameEntry
	wake    chan struct{}
	stop    chan struct{}
	resolve func(projectNameKey) string
}

func nameKey(p projectGrant) projectNameKey { return projectNameKey{p.Scope, p.Label, p.ProfileID} }

func newProjectNames(current grant, resolve func(user, workspace, profile string) string) *projectNames {
	if resolve == nil {
		return nil
	}
	n := &projectNames{entries: make(map[projectNameKey]*projectNameEntry), wake: make(chan struct{}, 1), stop: make(chan struct{})}
	n.resolve = func(k projectNameKey) string { return resolve(current.User, k.workspace, k.profile) }
	// Resolve only the selected project before entering the reader, so its group
	// has the friendly name immediately. Other projects load in the background.
	k := projectNameKey{current.Scope, current.Label, current.ProfileID}
	n.entries[k] = &projectNameEntry{name: n.resolve(k)}
	go n.run()
	return n
}

func (n *projectNames) close() {
	if n != nil {
		close(n.stop)
	}
}

func (n *projectNames) cached(projects []projectGrant) []projectGrant {
	if n == nil {
		return projects
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	live := make(map[projectNameKey]bool, len(projects))
	for i, p := range projects {
		k := nameKey(p)
		live[k] = true
		e := n.entries[k]
		if e == nil {
			e = &projectNameEntry{pending: true}
			n.entries[k] = e
		}
		projects[i].Name = e.name
		if projects[i].Name == "" {
			projects[i].Name = path.Base(p.Label)
		}
	}
	for k := range n.entries {
		if !live[k] {
			delete(n.entries, k)
		}
	}
	select {
	case n.wake <- struct{}{}:
	default:
	}
	return projects
}

func (n *projectNames) run() {
	// Preserve eventual project rename updates without reading every project on
	// every 25-second heartbeat. Production name reads have a one-second timeout.
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-ticker.C:
			n.mu.Lock()
			for _, e := range n.entries {
				e.pending = true
			}
			n.mu.Unlock()
		case <-n.wake:
		}
		for {
			select {
			case <-n.stop:
				return
			default:
			}
			n.mu.Lock()
			var k projectNameKey
			var pending *projectNameEntry
			for key, e := range n.entries {
				if e.pending {
					k, pending = key, e
					e.pending = false
					break
				}
			}
			n.mu.Unlock()
			if pending == nil {
				break
			}
			name := n.resolve(k)
			n.mu.Lock()
			// A removed/replaced grant cannot receive an old lookup's result.
			if n.entries[k] == pending {
				pending.name = name
			}
			n.mu.Unlock()
		}
	}
}
