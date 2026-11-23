package acpcli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type imageFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Prompt []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Data string `json:"data"`
			MIME string `json:"mimeType"`
		} `json:"prompt"`
	} `json:"params"`
}
type imageWire struct {
	bytes.Buffer
	rpc      *conn
	requests chan imageFrame
	largest  int
}

func (w *imageWire) Close() error                      { return nil }
func (w *imageWire) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *imageWire) Write(p []byte) (int, error) {
	w.largest = max(w.largest, len(p))
	n, _ := w.Buffer.Write(p)
	if bytes.Contains(p, []byte{'\n'}) {
		var request imageFrame
		if err := json.Unmarshal(w.Bytes(), &request); err != nil {
			return 0, err
		}
		w.Reset()
		w.requests <- request
	}
	return n, nil
}
func (w *imageWire) reply(id json.RawMessage, result json.RawMessage) {
	w.rpc.mu.Lock()
	ch := w.rpc.pending[string(id)]
	w.rpc.mu.Unlock()
	ch <- message{Result: result}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriter) Close() error                { return nil }

type imageReader struct {
	io.Reader
	closed chan struct{}
}

func (r *imageReader) Close() error { close(r.closed); return nil }

// Explicit native capability is the authorization to open image factories.
// Prepared readers close after write even while the model response is pending.
func TestImagesRequireCapabilityAndCloseAtWrite(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("image_capability=%t", allowed), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			wire := &imageWire{requests: make(chan imageFrame, 1)}
			rpc := newConn("image-chat", "test", wire)
			wire.rpc = rpc
			opened, skipped := 0, 0
			closed := make(chan struct{})
			pixels := strings.Repeat("pixels", 10000)
			input := llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
				opened++
				return &imageReader{strings.NewReader(pixels), closed}, "image/png", int64(len(pixels)), nil
			}, Skipped: func(string) { skipped++ }}
			delivery := llmtypes.WithNativeImageInputs(ctx, "image-chat", []llmtypes.NativeImageInput{input})
			params, cleanup, err := paramsWithImages(delivery, "acp-cli", "image-chat", map[string]any{"sessionId": "native", "prompt": []any{map[string]any{"type": "text", "text": "original file path"}}}, nil, allowed)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			done := make(chan error, 1)
			go func() { done <- rpc.call(ctx, "session/prompt", params, nil) }()
			var frame imageFrame
			select {
			case frame = <-wire.requests:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if frame.Method != "session/prompt" || frame.Params.Prompt[0].Text != "original file path" {
				t.Fatal("text/path lost")
			}
			if allowed {
				if opened != 1 || skipped != 0 || len(frame.Params.Prompt) != 2 {
					t.Fatal("native image capability not used")
				}
				image := frame.Params.Prompt[1]
				decoded, err := base64.StdEncoding.DecodeString(image.Data)
				if err != nil || string(decoded) != pixels || image.MIME != "image/png" || image.Type != "image" {
					t.Fatal("wrong native content block")
				}
				select {
				case <-closed:
				case <-ctx.Done():
					t.Fatal("reader held until model response")
				}
				if wire.largest >= len(pixels) {
					t.Fatal("image marshaled into a full pooled base64 buffer")
				}
			} else if opened != 0 || skipped != 1 || len(frame.Params.Prompt) != 1 {
				t.Fatal("missing/false capability opened an image or lost the notice")
			}
			select {
			case <-done:
				t.Fatal("model response did not remain pending")
			default:
			}
			wire.reply(frame.ID, json.RawMessage(`{"stopReason":"end_turn"}`))
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			retry, retryClose, err := paramsWithImages(delivery, "acp-cli", "image-chat", map[string]any{"prompt": []any{map[string]any{"type": "text", "text": "retry"}}}, nil, allowed)
			retryClose()
			if _, images := retry.(*imageParams); images || err != nil {
				t.Fatal("answered image replayed", err)
			}
		})
	}
	t.Run("stop_unblocks_partial_write", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		readEnd, writeEnd := io.Pipe()
		defer readEnd.Close()
		rpc := newConn("stopped-image", "test", writeEnd)
		closed := make(chan struct{})
		delivery := llmtypes.WithNativeImageInputs(ctx, "stopped-image", []llmtypes.NativeImageInput{{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
			return &imageReader{strings.NewReader("image"), closed}, "image/png", 5, nil
		}}})
		params, cleanup, err := paramsWithImages(delivery, "acp-cli", "stopped-image", map[string]any{"prompt": []any{}}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		done := make(chan error, 1)
		go func() { done <- rpc.call(context.Background(), "session/prompt", params, nil) }()
		// Read the start only, then stop while the large/private frame is blocked.
		if _, err := io.ReadFull(readEnd, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Stop lost cancellation", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stop hung behind frame write")
		}
		select {
		case <-closed:
		default:
			t.Fatal("stopped delivery retained its reader")
		}
	})
}

type repeatedByte byte

func (r repeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// The actual reader tolerates diagnostic/bad lines, strips only native content
// data before its event queue, and ends clearly on retained/wire/depth caps.
func TestImageEchoScopeMalformedLinesAndCaps(t *testing.T) {
	t.Run("stderr_payloads_are_omitted_before_character_limit", func(t *testing.T) {
		last := stderrLine("old diagnostic\nfailed: "+strings.Repeat("A", 1000)+" API_KEY=short-token detail\n", nil)
		if last != "failed: [omitted] [omitted] detail" {
			t.Fatal("stderr payload retained")
		}
		if got := stderrLine("last error é "+strings.Repeat("word ", 100), nil); len([]rune(got)) != 200 {
			t.Fatal("diagnostic exceeds character cap")
		}
		if got := stderrLine("bad credential short-value", []string{"PROVIDER_TOKEN=short-value"}); strings.Contains(got, "short-value") {
			t.Fatal("known credential retained")
		}
	})
	newRPC := func() *conn {
		exited := make(chan struct{})
		close(exited)
		c := newConn("reader-test", "test", discardWriter{})
		c.events, c.exited = make(chan message, 4), exited
		return c
	}
	prefix := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"native","update":{"sessionUpdate":"user_message_chunk","content":{"d\u0061ta":"`
	suffix := `","mimeType":"image/png","type":"image"}}}}` + "\n"
	tool := `{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","rawInput":{"content":{"type":"record","data":"keep private tool args"}}}}}` + "\n"
	source := io.MultiReader(strings.NewReader("diagnostic \"unfinished\n{bad json}\n"+prefix), io.LimitReader(repeatedByte('A'), 33<<20), strings.NewReader(suffix+tool))
	rpc := newRPC()
	rpc.read(source)
	if len(rpc.events) != 2 {
		t.Fatal("bad lines blocked following frames or native image exceeded old cap", len(rpc.events))
	}
	image := <-rpc.events
	if len(image.Params) > 1024 || !bytes.Contains(image.Params, []byte("[native image omitted]")) {
		t.Fatal("image bytes entered host events")
	}
	args := <-rpc.events
	if !bytes.Contains(args.Params, []byte("keep private tool args")) {
		t.Fatal("tool argument data was redacted")
	}
	for _, tc := range []struct {
		name   string
		source io.Reader
	}{
		{"retained", io.MultiReader(strings.NewReader(`{"text":"`), io.LimitReader(repeatedByte('A'), (32<<20)+1))},
		{"wire", io.MultiReader(strings.NewReader(prefix), io.LimitReader(repeatedByte('A'), 128<<20))},
		{"depth", strings.NewReader(strings.Repeat("[", 129))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRPC()
			done := make(chan struct{})
			go func() { r.read(tc.source); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("bounded reader hung")
			}
			select {
			case <-r.done:
			default:
				t.Fatal("cap left session running")
			}
			if len(r.events) != 0 || !strings.Contains(r.failure().Error(), "ACP") {
				t.Fatal("missing clear bounded frame error", r.failure())
			}
		})
	}
}
