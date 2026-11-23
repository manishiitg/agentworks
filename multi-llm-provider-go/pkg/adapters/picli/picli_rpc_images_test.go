package picli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type piImageFrameWriter struct {
	bytes.Buffer
	rpc     *piRPC
	frames  []map[string]any
	largest int
}

func (w *piImageFrameWriter) Close() error                      { return nil }
func (w *piImageFrameWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *piImageFrameWriter) Write(b []byte) (int, error) {
	w.largest = max(w.largest, len(b))
	n, _ := w.Buffer.Write(b)
	if bytes.Contains(b, []byte{'\n'}) {
		var request map[string]any
		if err := json.Unmarshal(w.Bytes(), &request); err != nil {
			return 0, err
		}
		w.Reset()
		w.frames = append(w.frames, request)
		w.rpc.mu.Lock()
		ch := w.rpc.pending[request["id"].(string)]
		w.rpc.mu.Unlock()
		ch <- piRPCRecord{Success: true, Data: json.RawMessage(`{"disposition":"queued"}`)}
	}
	return n, nil
}

type piImageReader struct {
	io.Reader
	closed *int
}

func (r *piImageReader) Close() error { *r.closed++; return nil }

// PLAT-746-1000: images follow the same pending-human/one-shot boundary as text;
// real prompt/steer RPC frames carry pixels but receipts and host events do not.
func TestPiRPCImagesPromptSteerAndSafeEcho(t *testing.T) {
	owner := "pi-image-chat"
	closed := 0
	data := strings.Repeat("pixels", 10000)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		return &piImageReader{strings.NewReader(data), &closed}, "image/png", int64(len(data)), nil
	}}
	withImage := func() context.Context {
		return llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{input})
	}
	wire := &piImageFrameWriter{}
	rpc := &piRPC{stdin: wire, pending: map[string]chan piRPCRecord{}, done: make(chan struct{})}
	wire.rpc = rpc
	session := &piRPCSession{rpc: rpc, turnID: "active"}
	piRPCSessions.Lock()
	piRPCSessions.sessions[owner] = session
	piRPCSessions.Unlock()
	defer func() { piRPCSessions.Lock(); delete(piRPCSessions.sessions, owner); piRPCSessions.Unlock() }()
	promptCtx := withImage()
	params, cleanup, err := piParamsWithImages(promptCtx, owner, map[string]any{"message": "Inspect this file"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = rpc.callPrepared(promptCtx, "prompt", params, nil); err != nil {
		t.Fatal(err)
	}
	cleanup()
	retry, retryClose, err := piParamsWithImages(promptCtx, owner, map[string]any{"message": "retry"}, nil)
	retryClose()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retry.(*piImageParams); ok {
		t.Fatal("retry replayed images")
	}
	if err = SendPiInteractiveInput(withImage(), owner, "A follow-up"); err != nil {
		t.Fatal(err)
	}
	if closed != 2 || len(wire.frames) != 2 {
		t.Fatal("image handles leaked or delivery lost")
	}
	for i, frame := range wire.frames {
		if frame["type"] != []string{"prompt", "steer"}[i] {
			t.Fatal("wrong native command")
		}
		images, ok := frame["images"].([]any)
		if !ok || len(images) != 1 {
			t.Fatal("missing native image")
		}
		image := images[0].(map[string]any)
		decoded, err := base64.StdEncoding.DecodeString(image["data"].(string))
		if err != nil || string(decoded) != data || image["mimeType"] != "image/png" || image["type"] != "image" {
			t.Fatal("image changed")
		}
	}
	if wire.largest >= len(data) {
		t.Fatal("base64 marshaled into one full frame")
	}
	if len(session.receipts) != 1 || session.receipts[0].text != "A follow-up" {
		t.Fatal("image entered text receipt")
	}
	skipped := false
	bad := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		return nil, "", 0, errors.New("access revoked")
	}, Skipped: func(string) { skipped = true }}})
	if err = SendPiInteractiveInput(bad, owner, "Text survives"); err != nil {
		t.Fatal(err)
	}
	if !skipped || wire.frames[2]["message"] != "Text survives" {
		t.Fatal("bad image swallowed text/notice")
	}
	late := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		session.mu.Lock()
		session.turnID = ""
		session.mu.Unlock()
		return io.NopCloser(strings.NewReader("pixels")), "image/png", 6, nil
	}}})
	if err = SendPiInteractiveInput(late, owner, "Too late"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatal("late input not returned to host queue", err)
	}
	if len(wire.frames) != 3 {
		t.Fatal("wrote into ended turn")
	}
	// Image data precedes type/mimeType, and an escaped data key must also be
	// removed. The >16MiB echo would overflow the former scanner before parsing.
	raw := `{"type":"message_end","message":{"role":"user","content":[{"d\u0061ta":"` + strings.Repeat("A", 17<<20) + `","type":"image","mimeType":"image/png"},{"type":"text","text":"keep \\\"data\\\" intact"}]}}` + "\n" + `{"type":"response","data":{"model":{"data":"keep"}}}` + "\n"
	reader := bufio.NewReader(strings.NewReader(raw))
	frame, err := readPiImageSafeFrame(reader)
	if err != nil || !json.Valid(frame) || len(frame) > 1024 || !bytes.Contains(frame, []byte("[native image omitted]")) {
		t.Fatal("native echo retained data or broke framing", err)
	}
	var record piRPCRecord
	if err = json.Unmarshal(frame, &record); err != nil {
		t.Fatal(err)
	}
	next, err := readPiImageSafeFrame(reader)
	if err != nil || !bytes.Contains(next, []byte(`"data":"keep"`)) {
		t.Fatal("ordinary command data was redacted", err)
	}
	// The native journal can contain the same large image, but durability still
	// compares only text and timestamp; the old 16MiB scanner rejected it.
	journal := filepath.Join(t.TempDir(), "native.jsonl")
	row := strings.Replace(strings.Replace(raw[:strings.IndexByte(raw, '\n')], `"type":"message_end"`, `"type":"message"`, 1), `"role":"user"`, `"role":"user","timestamp":123`, 1)
	if err = os.WriteFile(journal, []byte(row+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var echoed piRPCNativeMessage
	if err = json.Unmarshal(record.Message, &echoed); err != nil {
		t.Fatal(err)
	}
	if !piRPCUserPersisted(journal, piTranscriptText(piRPCParts(echoed.Content)), 123, 1) {
		t.Fatal("large-image user record lost its durable receipt")
	}
	// Answered images never become pending again, even if later text exists.
	imagePart := llmtypes.ImageContent{SourceType: "base64", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("pixels"))}
	history := []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{imagePart}}, llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "answered"), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "next")}
	if piHasPendingImages(ctx, owner, history) {
		t.Fatal("answered image counted as new input")
	}
	t.Setenv(EnvPiBinary, filepath.Join(t.TempDir(), "must-not-launch"))
	if _, err = NewPiCLIAdapter("", "google/test", nil).GenerateContent(ctx, history[:2]); err == nil || !strings.Contains(err.Error(), "prompt is empty") {
		t.Fatal("answered image started an empty native turn", err)
	}

}

// A native image echo must neither make an invalid stream hang nor allocate
// without bound. Failure closes the real RPC reader with a clear turn error.
func TestPiRPCImagesMalformedAndOversizedFrameEndsTurn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input func() io.Reader
	}{
		{"malformed_string", func() io.Reader { return strings.NewReader("{\"message\":{\"content\":[{\"data\":\"bad\n") }},
		{"unterminated_image", func() io.Reader { return strings.NewReader(`{"message":{"content":[{"data":"bad`) }},
		{"malformed_json", func() io.Reader { return strings.NewReader("{]\n") }},
		{"nesting_cap", func() io.Reader { return strings.NewReader(strings.Repeat("[", 129)) }},
		{"non_image_cap", func() io.Reader {
			return io.MultiReader(strings.NewReader(`{"text":"`), io.LimitReader(piRepeatedByte('A'), (16<<20)+1), strings.NewReader("\"}\n"))
		}},
		{"image_wire_cap", func() io.Reader {
			return io.MultiReader(strings.NewReader(`{"message":{"content":[{"data":"`), io.LimitReader(piRepeatedByte('A'), 128<<20), strings.NewReader("\"}]}}\n"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &piRPC{nativeID: "bad-frame-test", stdin: &piImageFrameWriter{}, done: make(chan struct{}), events: make(chan piRPCRecord, 1), stop: func() {}}
			returned := make(chan struct{})
			go func() { defer close(returned); rpc.read(tc.input()) }()
			select {
			case <-returned:
			case <-time.After(30 * time.Second):
				t.Fatal("RPC reader hung on invalid input")
			}
			select {
			case <-rpc.done:
			default:
				t.Fatal("invalid stream left the session live")
			}
			if rpc.failure() == nil || !strings.Contains(rpc.failure().Error(), "Pi RPC") {
				t.Fatal("missing clear protocol error", rpc.failure())
			}
			if len(rpc.events) != 0 {
				t.Fatal("invalid frame entered event queue")
			}
		})
	}
}

type piRepeatedByte byte

func (r piRepeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// PR184: filtering any field named content/images corrupted structured tool
// arguments. Only actual native message content may lose image data.
func TestPiRPCImageEchoPreservesToolArguments(t *testing.T) {
	lines := []string{
		`{"type":"tool_execution_start","toolCallId":"one","toolName":"write","args":{"content":[{"type":"record","data":"important user data"}],"images":[{"data":"also important"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"two","name":"write","arguments":{"content":[{"data":"nested content"}],"images":[{"data":"nested images"}]}}]}}`,
		`{"type":"tool_execution_end","result":{"content":[{"type":"text","text":"ok"}],"details":{"images":[{"data":"custom result"}]}}}`,
	}
	for _, line := range lines {
		rpc := &piRPC{stdin: &piImageFrameWriter{}, done: make(chan struct{}), events: make(chan piRPCRecord, 1), stop: func() {}}
		rpc.read(strings.NewReader(line + "\n"))
		select {
		case record := <-rpc.events:
			if string(record.Raw) != line {
				t.Fatal("tool argument/result bytes were altered", string(record.Raw))
			}
		default:
			t.Fatal("tool frame was not readable", rpc.failure())
		}
	}
}
