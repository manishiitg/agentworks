package browserrelay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// NewPersistent restores stable private connection codes and disconnected
// selections. A restart never restores browser authority or target IDs.
func NewPersistent(root string) (*Manager, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("Chrome selection state root must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	m := New()
	m.selectionPath = filepath.Join(root, "selected.json")
	m.credentialsPath = filepath.Join(root, "credentials.json")
	if err := m.loadCredentials(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(m.selectionPath)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	restorePlaceholder := func(routeKey, label string) *Binding {
		profile := ""
		for _, g := range m.pairs {
			resolved, ok := resolveProject(g, strings.TrimPrefix(routeKey, g.User+"\x00"))
			if ok && resolved.User+"\x00"+resolved.Scope == routeKey {
				profile = resolved.ProfileID
				break
			}
		}
		// Preserve fail-closed Code selections from the initial release, which
		// persisted labels but no profile or reusable credential.
		if profile == "" && strings.Contains("/"+label+"/", "/Chats/Code/projects/") {
			profile = "code"
		}
		return &Binding{key: routeKey, label: label, profile: profile, gate: make(chan struct{}, 1)}
	}
	// v2 state: browser slots, choices and labels.
	var state selectionState
	if err := json.Unmarshal(data, &state); err == nil && (state.Browsers != nil || state.Choices != nil || state.Labels != nil) {
		if len(state.Browsers) > 1000 || len(state.Labels) > 1000 {
			return nil, errors.New("too many saved Chrome selections")
		}
		for route, id := range state.Choices {
			m.choices[route] = id
		}
		for _, record := range state.Browsers {
			if !strings.Contains(record.Key, "\x00") || len(record.Key) > 8192 || record.BrowserID == "" {
				return nil, errors.New("invalid Chrome selection state")
			}
			b := restorePlaceholder(record.Key, state.Labels[record.Key])
			b.browserID, b.instanceID, b.name, b.customName = record.BrowserID, record.InstanceID, record.Name, record.CustomName
			b.createdAt = time.Now()
			m.bindings[fullKeyOf(record)] = b
		}
		for routeKey, label := range state.Labels {
			if !strings.Contains(routeKey, "\x00") || len(routeKey) > 8192 || len(label) > 8192 {
				return nil, errors.New("invalid Chrome selection state")
			}
			found := false
			for _, b := range m.bindings {
				if b.key == routeKey {
					found = true
					break
				}
			}
			if !found {
				b := restorePlaceholder(routeKey, label)
				m.bindings[routeKey+"\x00"] = b
			}
		}
		return m, nil
	}
	// v1 state: only the route-to-label map. Each route becomes an unnamed
	// placeholder slot the next connection revives, as before.
	var selected map[string]string
	if err := json.Unmarshal(data, &selected); err != nil {
		return nil, err
	}
	if len(selected) > 1000 {
		return nil, errors.New("too many saved Chrome selections")
	}
	for routeKey, label := range selected {
		if !strings.Contains(routeKey, "\x00") || len(routeKey) > 8192 || len(label) > 8192 {
			return nil, errors.New("invalid Chrome selection state")
		}
		m.bindings[routeKey+"\x00"] = restorePlaceholder(routeKey, label)
	}
	return m, nil
}

func fullKeyOf(record browserRecord) string {
	user, scope, _ := strings.Cut(record.Key, "\x00")
	return fullKey(user, scope, record.BrowserID)
}

// selectionState is the persisted form of the relay's browser slots: every
// remembered browser of every route, the recorded choice per route, and the
// workspace label per route (the v1 file was only that label map).
type selectionState struct {
	Browsers []browserRecord   `json:"browsers,omitempty"`
	Choices  map[string]string `json:"choices,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type browserRecord struct {
	Key        string `json:"key"`
	BrowserID  string `json:"browser_id"`
	InstanceID string `json:"instance_id,omitempty"`
	Name       string `json:"name,omitempty"`
	CustomName string `json:"custom_name,omitempty"`
}

// Caller holds m.mu. The state snapshots the manager's current slots; callers
// mutate first and persist before any externally visible effect, so a crash
// can at most restore an offline selection, never browser authority.
func (m *Manager) persistSelection() error {
	if m.selectionPath == "" {
		return nil
	}
	state := selectionState{Choices: map[string]string{}, Labels: map[string]string{}}
	for route, id := range m.choices {
		state.Choices[route] = id
	}
	seenLabels := map[string]bool{}
	for _, b := range m.bindings {
		if b.browserID != "" {
			state.Browsers = append(state.Browsers, browserRecord{Key: b.key, BrowserID: b.browserID, InstanceID: b.instanceID, Name: b.name, CustomName: b.customName})
		}
		if b.label != "" && !seenLabels[b.key] {
			state.Labels[b.key] = b.label
			seenLabels[b.key] = true
		}
	}
	sort.Slice(state.Browsers, func(i, j int) bool {
		return state.Browsers[i].Key+state.Browsers[i].BrowserID < state.Browsers[j].Key+state.Browsers[j].BrowserID
	})
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.selectionPath), ".selection-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), m.selectionPath)
}
