package browserrelay

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

// A project uses one main browser and may also use other saved browsers of the same person at the same time (owner,
// 2026-11-21: "can we have multiple browser connected.. instead of one at a time"). Each one is its own Chrome or Chrome
// profile, usually signed in to a different account; a browser command picks one by its name, and without a name the
// main one is used. Every browser keeps its own link to the project, so using one never disconnects another.

const maxAlsoBrowsers = 8

// CrewBrowser is one browser a project uses: its name, whether it is the main one and whether it is connected now.
type CrewBrowser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Main      bool   `json:"main,omitempty"`
	Connected bool   `json:"connected"`
}

// extraKey: a project's other browser keeps its own link, keyed user, browser, scope (the scope stays last, so the
// per-scope lookups by suffix still find it).
func extraKey(user, browserID, scope string) string {
	return user + "\x00" + browserID + "\x00" + scope
}

func contains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func without(ids []string, id string) []string {
	out := make([]string, 0, len(ids))
	for _, candidate := range ids {
		if candidate != id {
			out = append(out, candidate)
		}
	}
	return out
}

// Caller holds m.mu.
func (m *Manager) alsoUsesLocked(projectKey, id string) bool {
	return contains(m.alsoBrowsers[projectKey], id)
}

// Caller holds m.mu.
func (m *Manager) setAlsoLocked(projectKey string, ids []string) {
	if len(ids) == 0 {
		delete(m.alsoBrowsers, projectKey)
		return
	}
	m.alsoBrowsers[projectKey] = ids
}

// Caller holds m.mu. Forgets the project's other browsers and returns their links for the caller to close.
func (m *Manager) dropAlsoBrowsersLocked(user, scope string) []*Binding {
	var links []*Binding
	for _, id := range m.alsoBrowsers[key(user, scope)] {
		if b := m.bindings[extraKey(user, id, scope)]; b != nil {
			links = append(links, b)
			delete(m.bindings, extraKey(user, id, scope))
		}
	}
	if _, ok := m.alsoBrowsers[key(user, scope)]; ok {
		delete(m.alsoBrowsers, key(user, scope))
		_ = m.persistBrowsersLocked()
	}
	return links
}

// Caller holds m.mu.
func (m *Manager) alsoBrowsersLocked(user, scope string) []CrewBrowser {
	var out []CrewBrowser
	for _, id := range m.alsoBrowsers[key(user, scope)] {
		connected := false
		if b := m.bindings[extraKey(user, id, scope)]; b != nil {
			connected = b.Status().Connected
		}
		out = append(out, CrewBrowser{ID: id, Name: m.browserRecords[key(user, id)].Name, Connected: connected})
	}
	return out
}

// Caller holds m.mu. Asks that browser to open its own link to the project, when it is online.
func (m *Manager) requestAlsoLocked(user, scope, id string) bool {
	a := m.accountBrowsers[key(user, id)]
	if a == nil || !a.connected() {
		return false
	}
	if b := m.bindings[extraKey(user, id, scope)]; b != nil && b.Status().Connected {
		return true
	}
	log.Printf("[BROWSER_CONN] event=route_also user=%q installation=%q project=%q", user, id, scope)
	return a.write(envelope{Type: "connect-project", Scope: scope}) == nil
}

// AlsoUseBrowser adds one of the person's saved browsers to a project next to its main one.
func (m *Manager) AlsoUseBrowser(user, scope, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, exists := m.browserRecords[key(user, id)]
	if !exists || !r.Enabled {
		return errors.New("Connect this browser to your account first")
	}
	found := false
	for _, credential := range m.pairs {
		if credential.User == user {
			if _, ok := resolveProject(credential, scope); ok {
				found = true
				break
			}
		}
	}
	if !found {
		return errors.New("Open this project's Browser settings first")
	}
	k := key(user, scope)
	main := m.selectedBrowsers[k]
	if main == "" {
		// The first browser of a project is its main one.
		if err := m.selectBrowserLocked(user, scope, id); err != nil {
			return err
		}
		m.requestAccountProjectLocked(user, scope)
		return nil
	}
	if main == id || m.alsoUsesLocked(k, id) {
		return nil
	}
	if len(m.alsoBrowsers[k]) >= maxAlsoBrowsers {
		return fmt.Errorf("A project can use up to %d other browsers", maxAlsoBrowsers)
	}
	previous := m.alsoBrowsers[k]
	m.setAlsoLocked(k, append(append([]string(nil), previous...), id))
	if err := m.persistBrowsersLocked(); err != nil {
		m.setAlsoLocked(k, previous)
		return err
	}
	log.Printf("[BROWSER_CONN] event=also_use user=%q installation=%q brand=%s project=%q", user, id, r.Browser, scope)
	m.requestAlsoLocked(user, scope, id)
	notifyChange(k)
	return nil
}

// StopAlsoBrowser removes one of a project's other browsers; its tabs for this project close, the main one stays.
func (m *Manager) StopAlsoBrowser(user, scope, id string) error {
	m.mu.Lock()
	k := key(user, scope)
	previous := m.alsoBrowsers[k]
	if !contains(previous, id) {
		m.mu.Unlock()
		return nil
	}
	m.setAlsoLocked(k, without(previous, id))
	if err := m.persistBrowsersLocked(); err != nil {
		m.setAlsoLocked(k, previous)
		m.mu.Unlock()
		return err
	}
	b := m.bindings[extraKey(user, id, scope)]
	delete(m.bindings, extraKey(user, id, scope))
	m.mu.Unlock()
	if b != nil {
		b.closeWithReason("Stopped using this browser in this project")
	}
	log.Printf("[BROWSER_CONN] event=also_stop user=%q installation=%q project=%q", user, id, scope)
	notifyChange(k)
	return nil
}

// CrewBrowsers lists the browsers a project uses, the main one first.
func (m *Manager) CrewBrowsers(user, scope string) []CrewBrowser {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CrewBrowser
	if id := m.selectedBrowsers[key(user, scope)]; id != "" {
		connected := false
		if b := m.bindings[key(user, scope)]; b != nil {
			connected = b.Status().Connected
		}
		out = append(out, CrewBrowser{ID: id, Name: m.browserRecords[key(user, id)].Name, Main: true, Connected: connected})
	}
	return append(out, m.alsoBrowsersLocked(user, scope)...)
}

// LookupNamed returns the link of the project browser with this name (any case); an empty name is the main one.
func (m *Manager) LookupNamed(user, scope, name string) (*Binding, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		if b := m.Lookup(user, scope); b != nil {
			return b, nil
		}
		return nil, errors.New("BROWSER_NOT_SELECTED: choose a browser in this project's Browser panel")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	if id := m.selectedBrowsers[key(user, scope)]; id != "" {
		r := m.browserRecords[key(user, id)]
		if strings.EqualFold(r.Name, name) {
			if b := m.bindings[key(user, scope)]; b != nil {
				return b, nil
			}
		}
		names = append(names, r.Name)
	}
	for _, id := range m.alsoBrowsers[key(user, scope)] {
		r := m.browserRecords[key(user, id)]
		if strings.EqualFold(r.Name, name) {
			if b := m.bindings[extraKey(user, id, scope)]; b != nil {
				return b, nil
			}
			return nil, fmt.Errorf("CHROME_EXTENSION_DISCONNECTED: Your %s browser is not connected yet. Open it with the extension.", r.Name)
		}
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		return nil, errors.New("BROWSER_NOT_SELECTED: choose a browser in this project's Browser panel")
	}
	return nil, fmt.Errorf("BROWSER_NOT_IN_PROJECT: this project uses %s. Add another browser in its Browser panel", strings.Join(names, ", "))
}
