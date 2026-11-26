package nativereplies

import "testing"

func TestNativeReplyOwnershipSurvivesRestart(t *testing.T) {
	SetForegroundLookup(func(string, string) string { return "turn-a" })
	defer SetForegroundLookup(nil)
	dir := t.TempDir()
	if err := Configure(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Configure("") })
	for _, first := range []string{Foreground, Observer} {
		root := "root-" + first
		if owner, err := Claim("claude-code", "native", root, first); err != nil || owner != first {
			t.Fatalf("first=%s owner=%s err=%v", first, owner, err)
		}
		other := Observer
		if first == Observer {
			other = Foreground
		}
		if owner, err := Claim("claude-code", "native", root, other); err != nil || owner != first {
			t.Fatalf("second stole root: %s/%v", owner, err)
		}
	}
	if err := SettleForeground("claude-code", "native", "turn-a", []string{"root-foreground"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim("claude-code", "native", "unfinished", Foreground); err != nil {
		t.Fatal(err)
	}
	if err := Configure(dir); err != nil {
		t.Fatal(err)
	}
	if s := State("claude-code", "native", "root-foreground"); s.Owner != Foreground || !s.Committed {
		t.Fatal("committed root lost ownership")
	}
	if s := State("claude-code", "native", "unfinished"); s.Owner != Observer || s.Committed {
		t.Fatal("unfinished root was stranded")
	}
	if _, err := Claim("claude-code", "native", "failed", Foreground); err != nil {
		t.Fatal(err)
	}
	if err := SettleForeground("claude-code", "native", "turn-a", nil); err != nil {
		t.Fatal(err)
	}
	if State("claude-code", "native", "failed").Owner != Observer {
		t.Fatal("failed turn retained unpublished root")
	}
}

func TestNativeReplyClaimsRequireDeliveredRoots(t *testing.T) {
	_ = Configure("")
	defer Configure("")
	SetForegroundLookup(nil)
	root := ClaudeRoot{Session: "native", Root: "late", Helper: true}
	if !ForegroundMayPublish(root) || State("claude-code", "native", "late").Owner != "" {
		t.Fatal("non-turn reader acquired a claim")
	}
	SetForegroundLookup(func(string, string) string { return "turn-a" })
	defer SetForegroundLookup(nil)
	if !ForegroundMayPublish(root) {
		t.Fatal("open foreground could not claim")
	}
	if _, err := Claim("claude-code", "native", "included", Foreground); err != nil {
		t.Fatal(err)
	}
	if err := SettleForeground("claude-code", "native", "unrelated", []string{"late"}); err != nil {
		t.Fatal(err)
	}
	if State("claude-code", "native", "late").Committed {
		t.Fatal("another turn committed this root")
	}
	if err := SettleForeground("claude-code", "native", "turn-a", []string{"included"}); err != nil {
		t.Fatal(err)
	}
	if State("claude-code", "native", "late").Owner != Observer {
		t.Fatal("root omitted from delivered answer was stranded")
	}
	if !State("claude-code", "native", "included").Committed {
		t.Fatal("delivered root was not committed")
	}
	calls := 0
	SetForegroundLookup(func(string, string) string {
		calls++
		if calls == 1 {
			return "ending-turn"
		}
		return ""
	})
	if ForegroundMayPublish(ClaudeRoot{Session: "native", Root: "racing-root", Helper: true}) {
		t.Fatal("turn ended during claim but reader published unclaimed root")
	}
}

func TestClaudeNativeReplyLineage(t *testing.T) {
	var c ClaudeLineage
	for _, row := range []string{
		`{"type":"user","uuid":"human","sessionId":"native","origin":{"kind":"human"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"human"}`,
		`{"type":"user","uuid":"stop","parentUuid":"a1","origin":{"kind":"task-notification"},"queueTranscriptOnly":true}`,
	} {
		if c.Observe([]byte(row)).Helper {
			t.Fatal("stopped notification started a turn")
		}
	}
	c.Observe([]byte(`{"type":"user","uuid":"notice","sessionId":"native","parentUuid":"stop","origin":{"kind":"task-notification"},"turnOrigin":"task_notification"}`))
	root := c.Observe([]byte(`{"type":"assistant","uuid":"a2","parentUuid":"notice"}`))
	if root.Root != "notice" || !root.Helper || root.Session != "native" {
		t.Fatalf("wrong lineage: %+v", root)
	}
	if c.Observe([]byte(`{"type":"assistant","uuid":"late","parentUuid":"a1"}`)).Helper {
		t.Fatal("interleaved foreground branch was misattributed")
	}
}
