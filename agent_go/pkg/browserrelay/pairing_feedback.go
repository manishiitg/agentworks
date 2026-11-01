package browserrelay

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Account/browser routing was introduced in 0.5.0. Newer releases need not
// require an update unless they actually change the pairing protocol.
const MinimumExtensionVersion = "0.5.0"

type PairingFailure struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Version string `json:"extension_version,omitempty"`
	At      int64  `json:"at"`
}

func extensionVersionBefore(version, minimum string) bool {
	parse := func(value string) ([]int, bool) {
		if !validExtensionVersion(value) {
			return nil, false
		}
		parts := strings.Split(value, ".")
		if len(parts) < 2 || len(parts) > 4 {
			return nil, false
		}
		numbers := make([]int, 4)
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 {
				return nil, false
			}
			numbers[i] = n
		}
		return numbers, true
	}
	installed, ok := parse(version)
	required, valid := parse(minimum)
	if !ok || !valid {
		return false // An absent version alone is not proof of an old client.
	}
	for i := range installed {
		if installed[i] != required[i] {
			return installed[i] < required[i]
		}
	}
	return false
}

// PairingFailure returns only the authenticated account's recent setup error.
// It is ephemeral, contains no credential/project data, and expires in 10 min.
func (m *Manager) PairingFailure(user string) *PairingFailure {
	m.mu.Lock()
	defer m.mu.Unlock()
	failure, ok := m.pairingFailures[user]
	if !ok || time.Since(time.UnixMilli(failure.At)) > 10*time.Minute {
		delete(m.pairingFailures, user)
		return nil
	}
	return &failure
}

func (m *Manager) rejectPairing(conn *websocket.Conn, user, reason, message, version string) {
	m.rejectPairingWithCause(conn, user, reason, message, version, reason)
}

func (m *Manager) rejectPairingWithCause(conn *websocket.Conn, user, reason, message, version, cause string) {
	if reason == "extension_outdated" {
		message = ExtensionUpdateMessage()
	}
	if !validExtensionVersion(version) {
		version = ""
	}
	// No pairing code, credential, tab or account identity is written here.
	log.Printf("[BROWSER_PAIR] event=rejected reason=%s cause=%s extension_version=%q authenticated=%t", reason, cause, version, user != "")
	if user != "" {
		m.mu.Lock()
		if m.pairingFailures == nil {
			m.pairingFailures = make(map[string]PairingFailure)
		}
		for account, failure := range m.pairingFailures {
			if time.Since(time.UnixMilli(failure.At)) > 10*time.Minute {
				delete(m.pairingFailures, account)
			}
		}
		// Connection credentials are already bounded to 1000 accounts. Keep
		// expired/rotated accounts from growing this transient map indefinitely.
		if len(m.pairingFailures) >= 1000 {
			oldest, at := "", time.Now().UnixMilli()
			for account, failure := range m.pairingFailures {
				if failure.At <= at {
					oldest, at = account, failure.At
				}
			}
			delete(m.pairingFailures, oldest)
		}
		m.pairingFailures[user] = PairingFailure{Reason: reason, Message: message, Version: version, At: time.Now().UnixMilli()}
		m.mu.Unlock()
		notifyChange(key(user, ""))
	}
	_ = conn.WriteJSON(envelope{Type: "error", Reason: reason, Workspace: message})
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4002, reason), time.Now().Add(time.Second))
}

func (m *Manager) rejectOutdatedExtension(conn *websocket.Conn, user, version string) {
	m.rejectPairing(conn, user, "extension_outdated", "Update your browser extension to "+MinimumExtensionVersion+" or later. Download the extension, replace its folder and click Reload in your browser's Extensions page. Then copy a new connection code.", version)
}

// Unknown, revoked and denied credentials deliberately produce the same
// payload and close frame. Neither channel may reveal a code's validity.
func (m *Manager) rejectConnectionCode(conn *websocket.Conn, version, cause string) {
	m.rejectPairingWithCause(conn, "", "invalid_connection_code", "This browser could not connect with that code. In AgentWorks, open Browser connections and choose Connect browser, then finish in Browser Bridge. If it still fails, download the latest extension there, replace its folder and click Reload on the browser’s Extensions page, then try again.", version, cause)
}

func (m *Manager) connectionCodeStillOwned(token, user string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.pairs[token]
	return exists && user != "" && current.User == user
}
