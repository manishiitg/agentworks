package musecli

import (
	"strconv"
	"testing"
)

func museRunRecord(seq int, kind, extra string) string {
	return `{"sequence":` + strconv.Itoa(seq) + `,"payload":{"kind":"run","event":{"kind":"` + kind + `"` + extra + `}}}` + "\n"
}

// Escape in the running Muse pane writes a cancelled terminal and no reply: the retained turn must settle, or the chat
// stays Working (PLAT-861-228).
func TestMuseLogEndedWithoutReply(t *testing.T) {
	cancelled := museRunRecord(11, "started", "") + museRunRecord(12, "terminal", `,"terminal":"cancelled","reason":"cancelled during model step"`)
	if reason, ok := museLogEndedWithoutReply(cancelled, 10); !ok || reason != "cancelled" {
		t.Fatalf("cancelled turn: reason=%q ok=%v", reason, ok)
	}
	if _, ok := museLogEndedWithoutReply(cancelled, 12); ok {
		t.Fatal("a terminal before the turn's baseline must not settle it")
	}
	if _, ok := museLogEndedWithoutReply(cancelled+museRunRecord(13, "started", ""), 10); ok {
		t.Fatal("a run that started after the cancel is still working")
	}
	completed := museRunRecord(11, "started", "") + museRunRecord(12, "terminal", `,"terminal":"completed"`)
	if _, ok := museLogEndedWithoutReply(completed, 10); ok {
		t.Fatal("a completed turn settles through its reply, not as interrupted")
	}
}
