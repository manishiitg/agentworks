package musecli

import (
	"encoding/json"
	"testing"
)

// A Monitor that fires between the chat's turns makes Muse run a turn of its
// own; its reply must reach the chat with the Monitor that woke it, not only
// the log.
func TestMuseMSPWakeTurnReplyReachesTheHost(t *testing.T) {
	var got []MuseMSPReply
	SetMuseMSPReplyHandler(func(reply MuseMSPReply) { got = append(got, reply) })
	t.Cleanup(func() { SetMuseMSPReplyHandler(nil) })
	s := &museMSPSession{owner: "chat-1", sessionID: "native-1"}
	event := func(method string, params any) museRPCMessage {
		raw, _ := json.Marshal(params)
		return museRPCMessage{Method: method, Params: raw}
	}
	a := &MuseCLIAdapter{}
	a.handleIdleMuseMSP(s, event("turn/started", map[string]any{"turnId": "wake-1"}))
	a.handleIdleMuseMSP(s, event("item/completed", map[string]any{"item": map[string]any{"itemId": "i1", "kind": "agentMessage", "turnId": "wake-1", "text": "Build finished: 3 tests failed.", "wakeSourceDescriptions": []string{"CI build"}}}))
	a.handleIdleMuseMSP(s, event("turn/completed", map[string]any{"turnId": "wake-1", "terminal": "completed"}))
	if len(got) != 1 || got[0].Owner != "chat-1" || got[0].Text != "Build finished: 3 tests failed." || len(got[0].Sources) != 1 || got[0].Sources[0] != "CI build" {
		t.Fatalf("wake reply = %+v", got)
	}
	// A start notification can precede the foreground RPC response. Once the
	// response identifies that turn, even a late idle completion is not a wake.
	a.handleIdleMuseMSP(s, event("turn/started", map[string]any{"turnId": "foreground"}))
	s.mu.Lock()
	s.turnID = "foreground"
	s.lastForegroundTurnID = "foreground"
	delete(s.wake, "foreground") // same admission as turn/start's response
	s.turnID = ""                // foreground consumer already finished
	s.mu.Unlock()
	a.handleIdleMuseMSP(s, event("turn/completed", map[string]any{"turnId": "foreground", "terminal": "completed"}))
	if len(got) != 1 {
		t.Fatalf("foreground turn handed off as wake: %+v", got)
	}

	// PLAT-746-941: a completed native wake with no assistant text must still
	// reach the shared continuation queue; duplicate terminal events cannot wake twice.
	for _, terminal := range []string{"completed", "cancelled", "failed"} {
		a.handleIdleMuseMSP(s, event("turn/started", map[string]any{"turnId": terminal}))
		a.handleIdleMuseMSP(s, event("turn/completed", map[string]any{"turnId": terminal, "terminal": terminal}))
		a.handleIdleMuseMSP(s, event("turn/completed", map[string]any{"turnId": terminal, "terminal": terminal}))
	}
	if len(got) != 4 || got[1].Text != "" || got[1].Terminal != "completed" || got[2].Terminal != "cancelled" || got[3].Terminal != "failed" || len(s.wake) != 0 {
		t.Fatalf("empty/terminal wake handoff = %+v; pending=%d", got, len(s.wake))
	}

}

// Muse's inputTokens already includes the cached part; adding cacheRead again doubled every msp turn's prompt
// tokens and halved its cache share (Excellence, 3 Nov: 100.9M recorded against 50.8M in Muse's own file).
func TestMuseMSPTurnCountsCachedTokensOnce(t *testing.T) {
	a := &MuseCLIAdapter{modelID: "muse-model"}
	resp := a.museMSPResponse(&museMSPSession{sessionID: "s"}, "ok", "ok", "muse-model", 314134, 151, 313460, 0)
	gi := resp.Choices[0].GenerationInfo
	if gi.PromptTokens == nil || *gi.PromptTokens != 314134 {
		t.Fatalf("prompt tokens = %v, want 314134", gi.PromptTokens)
	}
	if gi.Additional["prompt_tokens_include_cache"] != true {
		t.Fatal("prompt tokens not marked as including the cached part")
	}
}
