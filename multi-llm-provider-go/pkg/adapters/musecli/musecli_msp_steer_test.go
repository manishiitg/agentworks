package musecli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// fakeMuseHost answers like `muse serve` over in-memory pipes. Its first turn
// ends right after the steer arrives; with rejectSteer the steer's rejection
// row reaches the journal only after turn/completed, as seen live.
type fakeMuseHost struct {
	t              *testing.T
	journal        string
	rejectSteer    bool
	in             *bufio.Scanner
	out            io.Writer
	mu             sync.Mutex
	starts         []string
	imageCounts    []int
	badImage       bool
	metadataEchoes int
}

func (h *fakeMuseHost) send(v any) {
	raw, _ := json.Marshal(v)
	if _, err := h.out.Write(append(raw, '\n')); err != nil {
		h.t.Errorf("host write: %v", err)
	}
}

func (h *fakeMuseHost) endTurn(turnID, reply string) {
	h.send(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"itemId": "item-" + turnID, "kind": "agentMessage", "turnId": turnID, "text": reply}}})
	h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"turnId": turnID, "terminal": "completed"}})
}

func (h *fakeMuseHost) serve() {
	for h.in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				CommandID string `json:"commandId"`
				Input     []struct {
					Text       string `json:"text"`
					Type       string `json:"type"`
					Base64Data string `json:"base64Data"`
					MediaType  string `json:"mediaType"`
				} `json:"input"`
			} `json:"params"`
		}
		if json.Unmarshal(h.in.Bytes(), &req) != nil {
			continue
		}
		images := 0
		for _, part := range req.Params.Input {
			if part.Type == "image" {
				images++
				decoded, err := base64.StdEncoding.DecodeString(part.Base64Data)
				if err != nil || string(decoded) != "image-pixels" || part.MediaType != "image/png" {
					h.mu.Lock()
					h.badImage = true
					h.mu.Unlock()
				}
			}
		}
		h.mu.Lock()
		h.imageCounts = append(h.imageCounts, images)
		h.mu.Unlock()
		if images > 0 {
			// Muse1.4.20 MSP views echo attachment metadata only, never base64.
			h.send(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"kind": "userMessage", "itemId": "image-input", "attachments": []map[string]any{{"attachmentId": "one", "type": "image", "mediaType": "image/png", "width": 2, "height": 2}}}}})
			h.mu.Lock()
			h.metadataEchoes++
			h.mu.Unlock()
		}
		switch req.Method {
		case "turn/start":
			h.mu.Lock()
			h.starts = append(h.starts, req.Params.Input[0].Text)
			n := len(h.starts)
			h.mu.Unlock()
			turnID := fmt.Sprintf("turn-%d", n)
			h.send(map[string]any{"method": "turn/started", "params": map[string]any{"turnId": turnID}})
			h.send(map[string]any{"id": req.ID, "result": map[string]any{"turnId": turnID}})
			if n > 1 {
				h.endTurn(turnID, "answer to the steer")
			}
		case "turn/steer":
			h.send(map[string]any{"id": req.ID, "result": map[string]any{}})
			h.endTurn("turn-1", "first answer")
			if h.rejectSteer {
				time.Sleep(100 * time.Millisecond)
				row := fmt.Sprintf(`{"sequence":2,"payload_type":"turn_submit.rejected","payload":{"kind":"turn_submit_rejected","record":{"command_id":%q,"reason":"already_terminal"}}}`+"\n", req.Params.CommandID)
				f, err := os.OpenFile(h.journal, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					h.t.Errorf("journal: %v", err)
					continue
				}
				// A large unrelated native input row must not hide the following
				// text-only rejection receipt (former32MiB scanner failure).
				_, _ = f.WriteString(`{"payload":{"input":"`)
				_, _ = io.CopyN(f, museRepeatedByte('A'), 33<<20)
				_, _ = f.WriteString("\"}}\n")
				_, _ = f.WriteString(row)
				_ = f.Close()
			}
		}
	}
}

// Seen live, 6 Nov: Muse answered turn/steer, ended the turn without reading the
// message and journalled turn_submit.rejected already_terminal, so the person's
// message showed as delivered and no turn ran it. A rejected steer must run as
// exactly one next turn; a steer the turn read must not run again.
func TestMuseMSPSteerRejectedAtTurnEndRunsOnceAsNextTurn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reject     bool
		wantStarts []string
		wantAnswer string
		revoke     bool
		wantImages []int
	}{
		{"rejected after the turn ended", true, []string{"first prompt", "late message"}, "first answer\n\nanswer to the steer", false, []int{1, 1, 1}},
		{"read by the turn", false, []string{"first prompt"}, "first answer", false, []int{1, 1}},
		{"rejected and access revoked", true, []string{"first prompt", "late message"}, "first answer\n\nanswer to the steer", true, []int{1, 1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wait := museMSPSteerVerdictWait
			museMSPSteerVerdictWait = 500 * time.Millisecond
			t.Cleanup(func() { museMSPSteerVerdictWait = wait })

			dataHome := t.TempDir()
			dir := filepath.Join(dataHome, "muse", "sessions", "2026", "11", "06", "native-1")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(dir, "session.jsonl")
			if err := os.WriteFile(journal, []byte(`{"sequence":1,"payload_type":"runtime.session","payload":{}}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			toHost, hostIn := io.Pipe()
			fromHost, hostOut := io.Pipe()
			host := &fakeMuseHost{t: t, journal: journal, rejectSteer: tc.reject, in: bufio.NewScanner(toHost), out: hostOut}
			go host.serve()

			rpc := &museRPC{stdin: hostIn, pending: map[string]chan museRPCMessage{}, events: make(chan museRPCMessage, 64), done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}}
			go rpc.read(fromHost)
			t.Cleanup(func() { rpc.finish(fmt.Errorf("test over")); _ = hostOut.Close() })

			owner := "chat-" + strings.ReplaceAll(tc.name, " ", "-")
			s := &museMSPSession{owner: owner, sessionID: "native-1", dataHome: dataHome, rpc: rpc}
			museMSPSessions.Lock()
			museMSPSessions.m[owner] = s
			museMSPSessions.Unlock()
			t.Cleanup(func() {
				museMSPSessions.Lock()
				delete(museMSPSessions.m, owner)
				museMSPSessions.Unlock()
			})
			a := &MuseCLIAdapter{}
			go a.pumpMuseMSP(s)

			var opened, closed, skipped atomic.Int32
			input := llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
				n := opened.Add(1)
				if tc.revoke && n >= 3 {
					return nil, "", 0, errors.New("access revoked")
				}
				return &museTestImageReader{Reader: strings.NewReader("image-pixels"), closed: &closed}, "image/png", 12, nil
			}, Skipped: func(string) { skipped.Add(1) }}
			withImage := func() context.Context {
				return llmtypes.WithNativeImageInputs(context.Background(), owner, []llmtypes.NativeImageInput{input})
			}
			steerErr := make(chan error, 1)
			go func() {
				for {
					s.mu.Lock()
					running := s.turnID != ""
					s.mu.Unlock()
					if running {
						steerErr <- SendMuseMSPInput(withImage(), owner, "late message")
						return
					}
					time.Sleep(time.Millisecond)
				}
			}()

			ctx, cancel := context.WithTimeout(withImage(), 10*time.Second)
			defer cancel()
			params := map[string]any{"commandId": museCommandID(), "sessionId": "native-1", "input": []map[string]any{{"type": "text", "text": "first prompt"}}}
			resp, err := a.runMuseMSPTurns(ctx, s, owner, params, false, &llmtypes.CallOptions{}, func(llmtypes.StreamChunk) {})
			if err != nil {
				t.Fatalf("turns: %v", err)
			}
			if err := <-steerErr; err != nil {
				t.Fatalf("steer: %v", err)
			}
			host.mu.Lock()
			starts := append([]string(nil), host.starts...)
			counts := append([]int(nil), host.imageCounts...)
			bad := host.badImage
			echoes := host.metadataEchoes
			host.mu.Unlock()
			if fmt.Sprint(counts) != fmt.Sprint(tc.wantImages) || bad || echoes < 2 {
				t.Fatalf("native images/metadata echoes: counts=%v bad=%t echoes=%d", counts, bad, echoes)
			}
			wantOpen := int32(2)
			wantClosed := int32(2)
			if tc.reject {
				wantOpen = 3
				if !tc.revoke {
					wantClosed = 3
				}
			}
			if opened.Load() != wantOpen || closed.Load() != wantClosed || tc.revoke != (skipped.Load() > 0) {
				t.Fatalf("factory recovery/resource counts opened=%d closed=%d skipped=%d", opened.Load(), closed.Load(), skipped.Load())
			}
			// Retrying this original context cannot attach its answered image.
			retry, retryClose, retryErr := museParamsWithImages(ctx, owner, params, nil)
			retryClose()
			if _, image := retry.(*museImageParams); retryErr != nil || image {
				t.Fatal("answered image replayed", retryErr)
			}
			if strings.Join(starts, "|") != strings.Join(tc.wantStarts, "|") {
				t.Fatalf("turns started = %q, want %q", starts, tc.wantStarts)
			}
			s.mu.Lock()
			pendingWakes := len(s.wake)
			s.mu.Unlock()
			if pendingWakes != 0 {
				t.Fatalf("foreground start became wake: %d", pendingWakes)
			}
			if got := resp.Choices[0].Content; got != tc.wantAnswer {
				t.Fatalf("answer = %q, want %q", got, tc.wantAnswer)
			}
		})
	}
}

type museTestImageReader struct {
	io.Reader
	closed *atomic.Int32
}

func (r *museTestImageReader) Close() error { r.closed.Add(1); return nil }

type museRepeatedByte byte

func (r museRepeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}
