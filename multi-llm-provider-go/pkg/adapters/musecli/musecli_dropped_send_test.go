package musecli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeMuseDroppedSendTree(t *testing.T) (dataHome, sessionLog string) {
	t.Helper()
	dataHome = t.TempDir()
	sessionLog = filepath.Join(dataHome, "muse", "sessions", "2026", "10", "22", "fixture", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionLog), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionLog, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return dataHome, sessionLog
}

// A stale notice, history echo, native queue or another edited message must
// never authorize replay. In particular the resume note is shared by turns;
// matching that prefix is insufficient to identify the current draft.
func TestMuseConfirmLeavesAcceptedChangedAndBusyDraftsAlone(t *testing.T) {
	pane := func(text string) string {
		return "────────────────\n❯ " + text + "\n────────────────\nmuse · fixture\n"
	}
	note := strings.Repeat("shared resume note ", 16)
	cases := []struct {
		name, prompt, screen string
		accepted             bool
	}{
		{"accepted with stale refusal", "hello world", "Message not sent — another run is still starting\n" + pane("hello world"), true},
		{"changed after shared prefix", note + "\n\nfirst request", pane(note + "\n\nuser edited the request"), false},
		{"history echo and empty editor", "hello world", "❯ hello world\nold answer\n" + pane(""), false},
		{"running", "hello world", pane("hello world") + "esc to interrupt", false},
		{"natively queued", "hello world", "Queued input: hello world\n" + pane("hello world"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			data, logPath := writeMuseDroppedSendTree(t)
			calls := filepath.Join(dir, "calls")
			screen := filepath.Join(dir, "screen")
			if err := os.WriteFile(screen, []byte(tc.screen), 0600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\ncase \"$1\" in capture-pane) cat '" + screen + "' ;; esac\n"
			if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			since := time.Now().Add(-time.Second)
			if tc.accepted {
				row := `{"sequence":1,"recorded_at":` + strconv.FormatInt(time.Now().UnixMicro(), 10) + `,"payload_type":"runtime.user_intent.accepted","payload":{"intent_id":"fixture","refill_blocks":[{"kind":"text","text":"hello world"}]}}` + "\n"
				if err := os.WriteFile(logPath, []byte(row), 0600); err != nil {
					t.Fatal(err)
				}
			}
			previous := museResubmitShortConfirm
			museResubmitShortConfirm = 1100 * time.Millisecond
			defer func() { museResubmitShortConfirm = previous }()
			if err := museConfirmSubmitOrResubmit(context.Background(), data, "fixture", tc.prompt, since, ""); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(calls)
			if strings.Contains(string(raw), "send-keys") {
				t.Fatalf("must not touch this input: %s", raw)
			}
		})
	}
}
