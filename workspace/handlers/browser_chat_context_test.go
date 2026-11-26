//go:build !windows

package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// PLAT-746-1005: stale page selections must not attach the next page, and a
// picked element must not expose the rest of the viewport. Real private IPC
// and PNG processing; the daemon boundary is faked, not a live-browser claim.
func TestBrowserChatContextRejectsStalePageAndCropsSelectedElement(t *testing.T) {
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", fmt.Sprintf("context-fixture-%d", time.Now().UnixNano()))
	session := "session-0123456789abcdef--browser"
	dir, err := browserconfig.EnsureManagedDirectory(session, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(browserconfig.ManagedSocketRoot()) })
	if err = os.WriteFile(filepath.Join(dir, session+".stream"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, session+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var generation, screenshots atomic.Int32
	var changeDuringCapture atomic.Bool
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				var request map[string]any
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				var data any
				success := true
				switch request["action"] {
				case "evaluate":
					if script, _ := request["script"].(string); !strings.HasPrefix(script, "("+browserContextScript+")(") {
						success = false
					}
					url := fmt.Sprintf("https://example.com/page-%d", generation.Load())
					data = map[string]any{"origin": url, "result": map[string]any{"document": url + "/document", "url": url, "width": 16, "height": 16, "selector": "#selected", "text": "Picked element", "rect": map[string]int{"x": 2, "y": 3, "width": 4, "height": 5}}}
				case "screenshot":
					screenshots.Add(1)
					path, _ := request["path"].(string)
					if filepath.Dir(path) != browserconfig.ArtifactDirForSession(session) || request["fullPage"] != false {
						success = false
						break
					}
					frame := image.NewNRGBA(image.Rect(0, 0, 16, 16))
					for y := 0; y < 16; y++ {
						for x := 0; x < 16; x++ {
							pixel := color.NRGBA{R: 255, A: 255}
							if x >= 2 && x < 6 && y >= 3 && y < 8 {
								pixel = color.NRGBA{B: 255, A: 255}
							}
							frame.SetNRGBA(x, y, pixel)
						}
					}
					var pngData bytes.Buffer
					if png.Encode(&pngData, frame) != nil || os.WriteFile(path, pngData.Bytes(), 0600) != nil {
						success = false
					}
					if changeDuringCapture.Load() {
						generation.Add(1)
					}
					data = map[string]any{"path": path}
				default:
					success = false
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": request["id"], "success": success, "data": data})
			}()
		}
	}()
	defer func() { _ = listener.Close(); <-serverDone }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe, err := readBrowserChatContext(ctx, session, models.BrowserChatContextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	x, y := float64(3), float64(4)
	pick := models.BrowserChatContextRequest{DocumentToken: probe.DocumentToken, X: &x, Y: &y}
	captured, err := readBrowserChatContext(ctx, session, pick)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := base64.StdEncoding.DecodeString(captured.ImageBase64)
	if err != nil {
		t.Fatal(err)
	}
	crop, err := png.Decode(bytes.NewReader(encoded))
	if err != nil || crop.Bounds().Dx() != 4 || crop.Bounds().Dy() != 5 {
		t.Fatalf("invalid crop: %v", err)
	}
	for yy := 0; yy < 5; yy++ {
		for xx := 0; xx < 4; xx++ {
			if color.NRGBAModel.Convert(crop.At(xx, yy)).(color.NRGBA) != (color.NRGBA{B: 255, A: 255}) {
				t.Fatal("crop leaked surrounding pixels")
			}
		}
	}
	generation.Add(1)
	if _, err = readBrowserChatContext(ctx, session, pick); err == nil || screenshots.Load() != 1 {
		t.Fatal("stale pick captured a different page")
	}
	probe, err = readBrowserChatContext(ctx, session, models.BrowserChatContextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	pick.DocumentToken = probe.DocumentToken
	changeDuringCapture.Store(true)
	if _, err = readBrowserChatContext(ctx, session, pick); err == nil {
		t.Fatal("navigation during capture returned a mismatched crop")
	}
	files, err := os.ReadDir(browserconfig.ArtifactDirForSession(session))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary viewport screenshots remain: %v", err)
	}
}
