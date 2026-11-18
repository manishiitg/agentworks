package llmtypes

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// #3131: small deltas are coalesced, a burst keeps its newest tail, and a
// command that runs on its own says so even before it prints again.
func TestBackgroundOutputStreamCoalescesAndBounds(t *testing.T) {
	var mu sync.Mutex
	var got []BackgroundOutput
	s := NewBackgroundOutputStream("codex-cli", "chat")
	s.interval = time.Hour // flushed by hand
	s.emit = func(o BackgroundOutput) { mu.Lock(); got = append(got, o); mu.Unlock() }

	s.Write("cmd", "", "a")
	s.Write("cmd", "", "b")
	s.Flush()
	if len(got) != 1 || got[0].Text != "ab" || got[0].Dropped || got[0].CanStop || got[0].Provider != "codex-cli" || got[0].Owner != "chat" {
		t.Fatalf("coalesced %+v", got)
	}
	s.Flush()
	if len(got) != 1 {
		t.Fatal("an empty flush emitted a chunk")
	}
	s.Write("cmd", "", strings.Repeat("é", BackgroundOutputChunkMax)+"END")
	s.Flush()
	big := got[1]
	if len(big.Text) > BackgroundOutputChunkMax || !big.Dropped || !strings.HasSuffix(big.Text, "END") || !strings.HasPrefix(big.Text, "é") {
		t.Fatalf("burst kept %d bytes dropped=%t", len(big.Text), big.Dropped)
	}
	s.Detach("cmd", "cmd")
	s.Flush()
	if c := got[2]; c.Text != "" || !c.CanStop || c.TaskID != "cmd" {
		t.Fatalf("detach %+v", c)
	}
	s.Write("cmd", "", "late")
	s.End("cmd")
	if c := got[3]; c.Text != "late" || !c.Done || c.CanStop {
		t.Fatalf("end %+v", c)
	}
	if len(s.Running()) != 0 {
		t.Fatal("ended command still followed")
	}
	s.End("never-started")
	if len(got) != 4 {
		t.Fatal("ending an unknown command emitted output")
	}
}

func TestBackgroundOutputStreamFollowsBoundedCommandsAndFlushesOnTimer(t *testing.T) {
	done := make(chan BackgroundOutput, 1)
	s := NewBackgroundOutputStream("codex-cli", "chat")
	s.interval = 10 * time.Millisecond
	s.emit = func(o BackgroundOutput) {
		select {
		case done <- o:
		default:
		}
	}
	for i := 0; i < backgroundOutputMaxCommands+5; i++ {
		s.Write(string(rune('A'+i)), "", "x")
	}
	if n := len(s.Running()); n != backgroundOutputMaxCommands {
		t.Fatalf("following %d commands", n)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timer never flushed")
	}
}

func TestEmitBackgroundOutputBoundsAndNeedsIdentity(t *testing.T) {
	var got []BackgroundOutput
	SetBackgroundOutputListener(func(o BackgroundOutput) { got = append(got, o) })
	t.Cleanup(func() { SetBackgroundOutputListener(nil) })
	EmitBackgroundOutput(BackgroundOutput{Owner: "chat", Text: "no tool"})
	EmitBackgroundOutput(BackgroundOutput{ToolCallID: "t", Text: "no owner"})
	EmitBackgroundOutput(BackgroundOutput{Owner: "chat", ToolCallID: "t", Text: strings.Repeat("x", BackgroundOutputChunkMax+10)})
	if len(got) != 1 || len(got[0].Text) != BackgroundOutputChunkMax || !got[0].Dropped {
		t.Fatalf("emitted %+v", got)
	}
	if chunk := BackgroundOutputChunk(got[0]); chunk.Type != StreamChunkTypeBackgroundOutput || chunk.ToolCallID != "t" || chunk.BackgroundOutput == nil {
		t.Fatalf("chunk %+v", chunk)
	}
}
