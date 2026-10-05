package browserrelay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	var selected map[string]string
	if err := json.Unmarshal(data, &selected); err != nil {
		return nil, err
	}
	if len(selected) > 1000 {
		return nil, errors.New("too many saved Chrome selections")
	}
	for key, label := range selected {
		if !strings.Contains(key, "\x00") || len(key) > 8192 || len(label) > 8192 {
			return nil, errors.New("invalid Chrome selection state")
		}
		profile := ""
		for _, g := range m.pairs {
			resolved, ok := resolveProject(g, strings.TrimPrefix(key, g.User+"\x00"))
			if ok && resolved.User+"\x00"+resolved.Scope == key {
				profile = resolved.ProfileID
				break
			}
		}
		// Preserve fail-closed Code selections from the initial release, which
		// persisted labels but no profile or reusable credential.
		if profile == "" && strings.Contains("/"+label+"/", "/Chats/Code/projects/") {
			profile = "code"
		}
		m.bindings[key] = &Binding{key: key, label: label, profile: profile, gate: make(chan struct{}, 1)}
	}
	return m, nil
}

// Caller holds m.mu. Write the selection before changing the live binding so a
// crash cannot restore a different browser or retain a deleted selection.
func (m *Manager) persistSelection(exclude string, extra *grant) error {
	if m.selectionPath == "" {
		return nil
	}
	selected := make(map[string]string, len(m.bindings))
	for key, b := range m.bindings {
		if key != exclude {
			selected[key] = b.label
		}
	}
	if extra != nil {
		selected[key(extra.User, extra.Scope)] = extra.Label
	}
	data, err := json.Marshal(selected)
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
