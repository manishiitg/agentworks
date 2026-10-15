package browserrelay

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// BrowserConnection describes the caller's browser installation, never its
// credential, account control endpoint, or another project's tabs.
type BrowserConnection struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Browser       string `json:"browser"`
	Version       string `json:"version,omitempty"`
	Connected     bool   `json:"connected"`
	Enabled       bool   `json:"enabled"`
	LastConnected int64  `json:"last_connected"`
}

type accountBrowser struct {
	mu     sync.Mutex
	conn   *websocket.Conn
	token  string
	closed atomic.Bool
}

func (a *accountBrowser) connected() bool { return !a.closed.Load() }
func (a *accountBrowser) write(e envelope) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return errors.New("Browser is offline")
	}
	a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return a.conn.WriteJSON(e)
}
func (a *accountBrowser) close() { a.closed.Store(true); a.conn.Close() }

func validBrowserID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' {
			return false
		}
	}
	return true
}
func browserBrand(brand string) string {
	switch brand {
	case "Edge":
		return "Edge"
	case "Chrome":
		return "Chrome"
	default:
		return "Chromium"
	}
}
func (m *Manager) browserListLocked(user string) []BrowserConnection {
	result := []BrowserConnection{}
	for k, r := range m.browserRecords {
		if !strings.HasPrefix(k, user+"\x00") {
			continue
		}
		a := m.accountBrowsers[k]
		r.Connected = r.Enabled && a != nil && a.connected()
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LastConnected == result[j].LastConnected {
			return result[i].ID < result[j].ID
		}
		return result[i].LastConnected > result[j].LastConnected
	})
	return result
}
func (m *Manager) Browsers(user string) []BrowserConnection {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.browserListLocked(user)
}
func (m *Manager) PairAccount(user string, reset bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token, err := m.connectionCode(user, "", "", "", reset)
	if err == nil && reset {
		for k, a := range m.accountBrowsers {
			if strings.HasPrefix(k, user+"\x00") {
				a.close()
			}
		}
		for k, b := range m.bindings {
			if strings.HasPrefix(k, user+"\x00") {
				b.closeWithReason("Account connection code reset")
			}
		}
	}
	return token, err
}

type browserState struct {
	Browsers map[string]BrowserConnection `json:"browsers"`
	Selected map[string]string            `json:"selected"`
}

func (m *Manager) loadBrowserState() error {
	raw, err := os.ReadFile(m.browserStatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state browserState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if len(state.Browsers) > 1000 || len(state.Selected) > 1000 {
		return errors.New("too many saved browser connections")
	}
	for k, r := range state.Browsers {
		if !strings.Contains(k, "\x00") || !validBrowserID(r.ID) || len(r.Name) > 120 || browserBrand(r.Browser) != r.Browser {
			return errors.New("invalid saved browser connection")
		}
		r.Connected = false
		m.browserRecords[k] = r
	}
	for k, id := range state.Selected {
		if !strings.Contains(k, "\x00") || !validBrowserID(id) {
			return errors.New("invalid saved browser choice")
		}
		m.selectedBrowsers[k] = id
	}
	return nil
}
func (m *Manager) persistBrowsersLocked() error {
	if m.browserStatePath == "" {
		return nil
	}
	raw, err := json.Marshal(browserState{Browsers: m.browserRecords, Selected: m.selectedBrowsers})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.browserStatePath), ".browsers-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), m.browserStatePath)
}

func (m *Manager) SelectBrowser(user, scope, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selectBrowserLocked(user, scope, id)
}
func (m *Manager) selectBrowserLocked(user, scope, id string) error {
	r, exists := m.browserRecords[key(user, id)]
	if !exists || !r.Enabled {
		return errors.New("Connect this browser to your account first")
	}
	var g grant
	found := false
	for _, credential := range m.pairs {
		if credential.User == user {
			if resolved, ok := resolveProject(credential, scope); ok {
				g = resolved
				found = true
				break
			}
		}
	}
	if !found {
		return errors.New("Open this project's Browser settings first")
	}
	k := key(user, scope)
	previous := m.selectedBrowsers[k]
	m.selectedBrowsers[k] = id
	if err := m.persistBrowsersLocked(); err != nil {
		if previous == "" {
			delete(m.selectedBrowsers, k)
		} else {
			m.selectedBrowsers[k] = previous
		}
		return err
	}
	if err := m.persistSelection("", &g); err != nil {
		return err
	}
	old := m.bindings[k]
	if old == nil || old.browserID != id {
		m.bindings[k] = &Binding{key: k, label: g.Label, profile: g.ProfileID, browserID: id, browserName: r.Name, gate: make(chan struct{}, 1)}
		if old != nil {
			old.closeWithReason("A different browser was selected for this project")
		}
	}
	log.Printf("[BROWSER_CONN] event=select user=%q installation=%q brand=%s project=%q", user, id, r.Browser, scope)
	notifyChange(k)
	return nil
}

// Caller holds m.mu. One project keeps one server-selected browser. Account
// sockets remain independent, so selecting Edge never disconnects Chrome.
func (m *Manager) hasAccountBrowserLocked(user string) bool {
	for k := range m.accountBrowsers {
		if strings.HasPrefix(k, user+"\x00") {
			return true
		}
	}
	return false
}

func (m *Manager) requestAccountProjectLocked(user, scope string) bool {
	k := key(user, scope)
	id := m.selectedBrowsers[k]
	if id == "" {
		for _, r := range m.browserListLocked(user) {
			if r.Connected {
				id = r.ID
				break
			}
		}
		if id == "" || m.selectBrowserLocked(user, scope, id) != nil {
			return false
		}
	}
	a := m.accountBrowsers[key(user, id)]
	if a == nil || !a.connected() {
		return false
	}
	if b := m.bindings[k]; b != nil && b.browserID == id && b.Status().Connected {
		return true
	}
	log.Printf("[BROWSER_CONN] event=route user=%q installation=%q brand=%s project=%q", user, id, m.browserRecords[key(user, id)].Browser, scope)
	return a.write(envelope{Type: "connect-project", Scope: scope}) == nil
}
func (m *Manager) DisconnectBrowser(user, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(user, id)
	r, ok := m.browserRecords[k]
	if !ok {
		return errors.New("This browser connection is unavailable")
	}
	previous := r
	r.Enabled = false
	m.browserRecords[k] = r
	if err := m.persistBrowsersLocked(); err != nil {
		m.browserRecords[k] = previous
		return err
	}
	if a := m.accountBrowsers[k]; a != nil {
		_ = a.write(envelope{Type: "account-disconnected"})
		a.close()
	}
	for bk, b := range m.bindings {
		if strings.HasPrefix(bk, user+"\x00") && b.browserID == id {
			b.closeWithReason("Browser disconnected")
		}
	}
	log.Printf("[BROWSER_CONN] event=disconnect user=%q installation=%q brand=%s project=%q", user, id, r.Browser, "")
	notifyChange(key(user, ""))
	return nil
}

func (m *Manager) serveAccountBrowser(conn *websocket.Conn, hello envelope, authorize func(string, string, string, string) error, displayName func(string, string, string) string) {
	if !validBrowserID(hello.BrowserID) {
		_ = conn.WriteJSON(envelope{Type: "error", Workspace: "Reload the browser extension and connect again."})
		return
	}
	m.mu.Lock()
	credential, exists := m.pairs[hello.Token]
	m.mu.Unlock()
	if !exists || credential.User == "" {
		_ = conn.WriteJSON(envelope{Type: "error", Workspace: "Connection code is invalid or was reset"})
		return
	}
	user := credential.User
	if authorize != nil && authorize(user, "", "", "") != nil {
		_ = conn.WriteJSON(envelope{Type: "error", Workspace: "This account is unavailable. Sign in to AgentWorks again."})
		return
	}
	projects := m.availableProjects(hello.Token, authorize)
	if displayName != nil {
		for i := range projects {
			projects[i].Name = displayName(user, projects[i].Label, projects[i].ProfileID)
		}
	}
	m.mu.Lock()
	current, ok := m.pairs[hello.Token]
	if !ok || current.User != user {
		m.mu.Unlock()
		return
	}
	k := key(user, hello.BrowserID)
	record, remembered := m.browserRecords[k]
	if hello.Resume && (!remembered || !record.Enabled) {
		m.mu.Unlock()
		_ = conn.WriteJSON(envelope{Type: "error", Workspace: "This browser was disconnected. Connect again to enable it."})
		return
	}
	if !remembered && len(m.browserRecords) >= 1000 {
		m.mu.Unlock()
		return
	}
	brand := browserBrand(hello.Browser)
	record.ID = hello.BrowserID
	record.Browser = brand
	record.Enabled = true
	record.LastConnected = time.Now().UnixMilli()
	if validExtensionVersion(hello.Version) {
		record.Version = hello.Version
	}
	if record.Name == "" {
		record.Name = brand
		count := 0
		for existing, r := range m.browserRecords {
			if strings.HasPrefix(existing, user+"\x00") && r.Browser == brand {
				count++
			}
		}
		if count > 0 {
			record.Name = brand + " " + strconv.Itoa(count+1)
		}
	}
	m.browserRecords[k] = record
	if err := m.persistBrowsersLocked(); err != nil {
		m.mu.Unlock()
		_ = conn.WriteJSON(envelope{Type: "error", Workspace: "Could not remember this browser. Try connecting again."})
		return
	}
	a := &accountBrowser{conn: conn, token: hello.Token}
	old := m.accountBrowsers[k]
	m.accountBrowsers[k] = a
	if old != nil {
		old.close()
	}
	// Migration retains only already selected and authorized legacy projects.
	for _, scope := range hello.ResumeScopes {
		if len(hello.ResumeScopes) > 1000 {
			break
		}
		if m.selectedBrowsers[key(user, scope)] != "" || m.bindings[key(user, scope)] == nil {
			continue
		}
		if _, ok := resolveProject(current, scope); ok {
			_ = m.selectBrowserLocked(user, scope, hello.BrowserID)
		}
	}
	m.mu.Unlock()
	log.Printf("[BROWSER_CONN] event=connect user=%q installation=%q brand=%s project=%q", user, hello.BrowserID, brand, "")
	notifyChange(key(user, ""))
	defer func() {
		a.close()
		m.mu.Lock()
		if m.accountBrowsers[k] == a {
			for bk, b := range m.bindings {
				if strings.HasPrefix(bk, user+"\x00") && b.browserID == hello.BrowserID {
					b.close()
				}
			}
		}
		m.mu.Unlock()
		log.Printf("[BROWSER_CONN] event=offline user=%q installation=%q brand=%s project=%q", user, hello.BrowserID, brand, "")
		notifyChange(key(user, ""))
	}()
	if a.write(envelope{Type: "account-paired", BrowserID: hello.BrowserID, Browser: brand, Projects: projects, Diagnostics: true}) != nil {
		return
	}
	m.mu.Lock()
	for bk, id := range m.selectedBrowsers {
		if id == hello.BrowserID && strings.HasPrefix(bk, user+"\x00") {
			m.requestAccountProjectLocked(user, strings.TrimPrefix(bk, user+"\x00"))
		}
	}
	m.mu.Unlock()
	for {
		conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		var e envelope
		if conn.ReadJSON(&e) != nil {
			return
		}
		if e.Type == "disconnect" {
			_ = m.DisconnectBrowser(user, hello.BrowserID)
			return
		}
		if e.Type == "select-project" || e.Type == "disconnect-project" {
			m.mu.Lock()
			credential, exists := m.pairs[hello.Token]
			g, granted := resolveProject(credential, e.Scope)
			selected := m.selectedBrowsers[key(user, e.Scope)]
			m.mu.Unlock()
			if !exists || credential.User != user || !granted || authorize != nil && authorize(user, g.Scope, g.Label, g.ProfileID) != nil {
				_ = a.write(envelope{Type: "project-error", Workspace: "This project cannot use your browser. Open its Browser settings to check access."})
				continue
			}
			if e.Type == "disconnect-project" {
				if selected == hello.BrowserID {
					_ = m.Disconnect(user, e.Scope)
				}
				continue
			}
			if err := m.SelectBrowser(user, e.Scope, hello.BrowserID); err != nil {
				_ = a.write(envelope{Type: "project-error", Workspace: "Could not choose this browser. Try again from the project's Browser settings."})
				continue
			}
			m.RequestProjectConnection(user, e.Scope)
			continue
		}
		if e.Type != "ping" {
			return
		}
		if authorize != nil && authorize(user, "", "", "") != nil {
			return
		}
		m.mu.Lock()
		current, valid := m.pairs[hello.Token]
		valid = valid && current.User == user && m.browserRecords[k].Enabled && m.accountBrowsers[k] == a
		m.mu.Unlock()
		if !valid {
			return
		}
		projects = m.availableProjects(hello.Token, authorize)
		if displayName != nil {
			for i := range projects {
				projects[i].Name = displayName(user, projects[i].Label, projects[i].ProfileID)
			}
		}
		if a.write(envelope{Type: "pong", Projects: projects}) != nil {
			return
		}
	}
}
