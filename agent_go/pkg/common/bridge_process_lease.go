package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

const processBridgeTokenPrefix = "mcpp1."

type processBridgeCredential struct {
	token  string
	active bool
}
type processBridgeSlot struct {
	enabled bool
	current *processBridgeCredential
}

// Guarded by bridgeTokenMu with the signing key. Nothing survives a server
// restart or key reset. Tokens exist before launch, but authenticate only after
// the process-start callback; constructor failures cannot leave live authority.
var processBridgeSlots = map[string]*processBridgeSlot{}
var processBridgeTokens = map[[32]byte]string{}

func init() { codexcli.AppBridgeCredentialForOwner = acquireProcessBridgeCredential }

func resetProcessBridgeCredentialsLocked() {
	processBridgeSlots = map[string]*processBridgeSlot{}
	processBridgeTokens = map[[32]byte]string{}
}

// ConfigureProcessBridgeCredential is called only by an admitted host main
// query, never by a child or model input. Other providers retain ordinary
// session authentication; a previously leased token is invalidated on switch.
func ConfigureProcessBridgeCredential(session string, enabled bool) {
	session = strings.TrimSpace(session)
	if session == "" {
		return
	}
	bridgeTokenMu.Lock()
	defer bridgeTokenMu.Unlock()
	slot := processBridgeSlots[session]
	if !enabled || len(bridgeTokenKey) == 0 {
		if slot != nil {
			dropProcessBridgeCredentialLocked(session, slot, "provider_changed")
			delete(processBridgeSlots, session)
		}
		return
	}
	if slot == nil {
		slot = &processBridgeSlot{}
		processBridgeSlots[session] = slot
	}
	slot.enabled = true
}

func dropProcessBridgeCredentialLocked(session string, slot *processBridgeSlot, reason string) {
	if slot.current == nil {
		return
	}
	delete(processBridgeTokens, sha256.Sum256([]byte(slot.current.token)))
	slot.current = nil
	log.Printf("[CODING_BRIDGE] session=%s action=credential_revoked reason=%s", session, reason)
}

// RevokeProcessBridgeCredential disables acquisition until a fresh host query
// has been admitted. A late process cleanup cannot remove a newer generation.
func RevokeProcessBridgeCredential(session, reason string) {
	bridgeTokenMu.Lock()
	defer bridgeTokenMu.Unlock()
	if slot := processBridgeSlots[session]; slot != nil {
		dropProcessBridgeCredentialLocked(session, slot, reason)
		slot.enabled = false
	}
}

func processBridgeCredentialLocked(session string) (*processBridgeSlot, *processBridgeCredential, bool) {
	slot, managed := processBridgeSlots[session]
	if !managed {
		return nil, nil, false
	}
	if !slot.enabled || len(bridgeTokenKey) == 0 {
		return slot, nil, true
	}
	if slot.current == nil {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			panic("bridge process credential: entropy unavailable")
		}
		slot.current = &processBridgeCredential{token: processBridgeTokenPrefix + hex.EncodeToString(raw[:])}
		processBridgeTokens[sha256.Sum256([]byte(slot.current.token))] = session
		log.Printf("[CODING_BRIDGE] session=%s action=credential_created reason=process_generation", session)
	}
	return slot, slot.current, true
}

func processBridgeTokenForSession(session string) (string, bool) {
	bridgeTokenMu.Lock()
	defer bridgeTokenMu.Unlock()
	_, credential, managed := processBridgeCredentialLocked(session)
	if credential == nil {
		return "", managed
	}
	return credential.token, managed
}

func verifyProcessBridgeToken(token string) (string, bool) {
	bridgeTokenMu.RLock()
	defer bridgeTokenMu.RUnlock()
	session, ok := processBridgeTokens[sha256.Sum256([]byte(token))]
	slot := processBridgeSlots[session]
	return session, ok && len(bridgeTokenKey) > 0 && slot != nil && slot.enabled && slot.current != nil && slot.current.active
}

func acquireProcessBridgeCredential(session, scope string) *codexcli.AppBridgeCredential {
	// Helpers sharing a chat keep their exact per-build virtual scope and must
	// never own/release the host process's credential generation.
	if scope != session+":vt:host-main" {
		return nil
	}
	bridgeTokenMu.Lock()
	slot, credential, _ := processBridgeCredentialLocked(session)
	bridgeTokenMu.Unlock()
	if credential == nil {
		return nil
	}
	// A real process replacement minted after wrapper setup must refresh the
	// server-side shell's bridge auth too; never leave its copied env stale.
	sessionShellConfigsMu.Lock()
	if cfg := sessionShellConfigs[session]; cfg != nil && cfg.Env["MCP_SESSION_ID"] == session && cfg.Env["MCP_API_TOKEN"] != credential.token {
		next := cloneSessionShellConfig(cfg)
		next.Env["MCP_API_TOKEN"] = credential.token
		if _, present := next.Env["MCP_AUTH"]; present {
			next.Env["MCP_AUTH"] = "Authorization: Bearer " + credential.token
		}
		sessionShellConfigs[session] = next
	}
	sessionShellConfigsMu.Unlock()

	return &codexcli.AppBridgeCredential{
		Token: credential.token,
		Started: func() {
			bridgeTokenMu.Lock()
			defer bridgeTokenMu.Unlock()
			if processBridgeSlots[session] == slot && slot.current == credential && slot.enabled {
				credential.active = true
				log.Printf("[CODING_BRIDGE] session=%s action=credential_active reason=process_started", session)
			}
		},
		Release: func() {
			bridgeTokenMu.Lock()
			defer bridgeTokenMu.Unlock()
			if processBridgeSlots[session] == slot && slot.current == credential {
				dropProcessBridgeCredentialLocked(session, slot, "process_closed")
			}
		},
	}
}

// DiscardPendingProcessBridgeCredential cleans up setup that failed before a
// native process could start. It cannot revoke a live or newer generation.
func DiscardPendingProcessBridgeCredential(session, token string) {
	bridgeTokenMu.Lock()
	defer bridgeTokenMu.Unlock()
	if slot := processBridgeSlots[session]; slot != nil && slot.current != nil && !slot.current.active && slot.current.token == token {
		dropProcessBridgeCredentialLocked(session, slot, "setup_failed")
	}
}
