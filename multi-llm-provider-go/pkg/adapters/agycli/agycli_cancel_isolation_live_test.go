package agycli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live cancellation + isolation P0 proofs for agy. Cancellation: killing the
// context mid-turn fails the turn without poisoning later turns.
// Parallel isolation: two concurrent turns in separate workdirs keep their
// own canaries and conversations.

func TestAgyCLIRealCancellationContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	adapter := NewAgyCLIAdapter("", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not use tools."),
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Write a 2000-word essay on the history of concrete. Be verbose and slow."),
		}, WithWorkingDir(workDir))
		done <- err
	}()
	time.Sleep(3 * time.Second)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled turn returned no error")
		}
		t.Logf("cancelled turn error (expected): %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("cancelled turn did not return within 60s")
	}

	freshCtx, freshCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer freshCancel()
	token := "AGY_CANCEL_FRESH_" + agyRandomHex(t, 4)
	resp, err := adapter.GenerateContent(freshCtx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+token),
	}, WithWorkingDir(workDir))
	if err != nil {
		t.Fatalf("fresh turn after cancel error = %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, token) {
		t.Fatalf("fresh turn content = %q, want %s", content, token)
	}
}

// TestAgyCLIRealPersistentCancelReuseContract proves that stopping a turn in
// the persistent sidecar does not leave its owner unable to send a new turn.
func TestAgyCLIRealPersistentCancelReuseContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	agyKeyModeForTest(t)
	workDir := t.TempDir()
	agyTrustWorkdirForTest(t, workDir)
	owner := "agy-cancel-reuse-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })
	adapter := NewAgyCLIAdapter("", "", nil)

	turnCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := adapter.GenerateContent(turnCtx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Write a 3000-word essay about the history of glass. Be thorough and do not use tools."),
		}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner))
		done <- err
	}()

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer probeCancel()
	for {
		select {
		case err := <-done:
			t.Fatalf("slow turn ended before interruption: %v", err)
		case <-probeCtx.Done():
			t.Fatal("sidecar turn never reached its busy state")
		default:
		}
		if session, ok := activeAgyInteractiveSession(owner); ok {
			pane, err := captureAgyPane(probeCtx, session.tmuxSessionName)
			if err == nil && strings.Contains(pane, agyPaneBusyMarker) {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled sidecar turn returned no error")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("canceled sidecar turn did not return")
	}

	freshCtx, freshCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer freshCancel()
	token := "AGY_REUSE_" + agyRandomHex(t, 4)
	resp, err := adapter.GenerateContent(freshCtx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+token),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner))
	if err != nil {
		t.Fatalf("new message under canceled owner: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 || !strings.Contains(resp.Choices[0].Content, token) {
		t.Fatalf("new message under canceled owner did not answer %q: %#v", token, resp)
	}
}

func TestAgyCLIRealParallelIsolationContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	adapter := NewAgyCLIAdapter("", "", nil)
	tokenA := "AGY_ISO_A_" + agyRandomHex(t, 4)
	tokenB := "AGY_ISO_B_" + agyRandomHex(t, 4)

	type result struct {
		token   string
		content string
		convID  string
		err     error
	}
	run := func(token string) result {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not use tools. Reply with exactly what the user asks for."),
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+token),
		}, WithWorkingDir(t.TempDir()))
		if err != nil {
			return result{token: token, err: err}
		}
		handle, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
		return result{token: token, content: resp.Choices[0].Content, convID: handle.NativeSessionID}
	}

	results := make(chan result, 2)
	go func() { results <- run(tokenA) }()
	go func() { results <- run(tokenB) }()
	first, second := <-results, <-results
	if first.err != nil {
		t.Fatalf("parallel turn A error = %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("parallel turn B error = %v", second.err)
	}
	if !strings.Contains(first.content, first.token) {
		t.Fatalf("turn A content = %q, want own token %s", first.content, first.token)
	}
	if !strings.Contains(second.content, second.token) {
		t.Fatalf("turn B content = %q, want own token %s", second.content, second.token)
	}
	if first.convID == "" || first.convID == second.convID {
		t.Fatalf("conversation ids not isolated: %q vs %q", first.convID, second.convID)
	}
}
