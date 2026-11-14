package common

import "testing"

// PLAT-746-941: per-build/hourly bridge auth restarted a warm Codex process.
// A host process owns one exact generation; stale cleanup and children cannot
// revoke its replacement, and a token without a live process cannot authenticate.
func TestProcessBridgeCredentialLifetimeAndIsolation(t *testing.T) {
	SetBridgeTokenSecret("fixture-server")
	t.Cleanup(func() { SetBridgeTokenSecret("") })
	ConfigureProcessBridgeCredential("chat", true)
	first := BridgeTokenForSession("chat")
	if first == "" || first != BridgeTokenForSession("chat") {
		t.Fatal("wrapper rebuild changed process credential")
	}
	if _, ok := VerifyBridgeToken(first); ok {
		t.Fatal("pending setup authenticated without a process")
	}
	if acquireProcessBridgeCredential("chat", "chat:vt:child") != nil || acquireProcessBridgeCredential("unknown", "unknown:vt:host-main") != nil {
		t.Fatal("child or unknown owner acquired host lease")
	}
	lease := acquireProcessBridgeCredential("chat", "chat:vt:host-main")
	if lease == nil {
		t.Fatal("admitted host lease missing")
	}
	lease.Started()
	if owner, ok := VerifyBridgeToken(first); !ok || owner != "chat" {
		t.Fatal("live process credential refused or misbound")
	}
	DiscardPendingProcessBridgeCredential("chat", first)
	if _, ok := VerifyBridgeToken(first); !ok {
		t.Fatal("setup cleanup revoked a live process")
	}
	lease.Release()
	if _, ok := VerifyBridgeToken(first); ok {
		t.Fatal("exited process kept authority")
	}
	second := acquireProcessBridgeCredential("chat", "chat:vt:host-main")
	if second == nil || second.Token == first {
		t.Fatal("replacement reused revoked credential")
	}
	second.Started()
	bridgeTokenMu.RLock()
	count := len(processBridgeTokens)
	bridgeTokenMu.RUnlock()
	if count != 1 {
		t.Fatal("old generation remained in credential index")
	}
	lease.Release()
	if _, ok := VerifyBridgeToken(second.Token); !ok {
		t.Fatal("old cleanup revoked replacement")
	}
	RevokeProcessBridgeCredential("chat", "stop")
	if _, ok := VerifyBridgeToken(second.Token); ok {
		t.Fatal("Stop left process authority")
	}
	if BridgeTokenForSession("chat") != "" || acquireProcessBridgeCredential("chat", "chat:vt:host-main") != nil {
		t.Fatal("stopped process recreated authority without admission")
	}
	ConfigureProcessBridgeCredential("chat", true)
	pending := BridgeTokenForSession("chat")
	DiscardPendingProcessBridgeCredential("chat", pending)
	if _, ok := VerifyBridgeToken(pending); ok {
		t.Fatal("failed setup left authority")
	}
	next := acquireProcessBridgeCredential("chat", "chat:vt:host-main")
	next.Started()
	SetBridgeTokenSecret("fresh-server")
	if _, ok := VerifyBridgeToken(next.Token); ok {
		t.Fatal("server restart retained process generation")
	}
	ConfigureProcessBridgeCredential("chat", true)
	switched := acquireProcessBridgeCredential("chat", "chat:vt:host-main")
	switched.Started()
	ConfigureProcessBridgeCredential("chat", false)
	if _, ok := VerifyBridgeToken(switched.Token); ok {
		t.Fatal("provider switch retained old process credential")
	}
	ordinary := BridgeTokenForSession("chat")
	if owner, ok := VerifyBridgeToken(ordinary); !ok || owner != "chat" {
		t.Fatal("ordinary session auth changed")
	}
}
