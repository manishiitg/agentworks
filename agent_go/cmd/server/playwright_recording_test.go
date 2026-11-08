package server

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPlaywrightReplayPlaybackDownloadAndDelete(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required for real replay encoding")
	}
	if err := exec.Command("ffmpeg", "-version").Run(); err != nil {
		t.Skipf("ffmpeg required for real replay encoding: %v", err)
	}
	api, server := playwrightTestServer(t)
	producer, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/s/run/tools/browser/live", http.Header{"Authorization": []string{"Bearer producer-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	id := readPlaywrightType(t, producer, "registered")["browser_session"].(string)
	// The recording is created just after "registered" is sent; wait for it.
	var rec *playwrightRecording
	for start := time.Now(); rec == nil; time.Sleep(10 * time.Millisecond) {
		api.playwrightLive.Lock()
		rec = api.playwrightLive.recordings[id]
		api.playwrightLive.Unlock()
		if rec == nil && time.Since(start) > 5*time.Second {
			t.Fatal("recording was not created after registration")
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.Set(0, 0, color.White)
	var jpg bytes.Buffer
	if err = jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	if err = producer.WriteJSON(map[string]interface{}{"type": "frame", "data": base64.StdEncoding.EncodeToString(jpg.Bytes()), "metadata": map[string]int{"deviceWidth": 64, "deviceHeight": 48}}); err != nil {
		t.Fatal(err)
	}
	producer.Close()
	deadline := time.Now().Add(15 * time.Second)
	for {
		state, problem := rec.status()
		if state == "ready" {
			break
		}
		if state == "error" || time.Now().After(deadline) {
			t.Fatalf("replay %s: %s", state, problem)
		}
		time.Sleep(20 * time.Millisecond)
	}
	request := func(method, user, workspace, body, extra string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+"/api/browser/live/"+id+"/recording?workspace_path="+workspace+extra, strings.NewReader(body))
		req.Header.Set("X-Test-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	for _, method := range []string{"GET", "POST"} {
		for _, scope := range [][2]string{{"bob", "Workflow/test"}, {"alice", "Workflow/other"}} {
			resp := request(method, scope[0], scope[1], `{"action":"delete"}`, "")
			if resp.StatusCode != 404 {
				t.Fatalf("cross-scope %s: %d", method, resp.StatusCode)
			}
		}
	}
	for _, extra := range []string{"", "&download=1"} {
		resp := request("GET", "alice", "Workflow/test", "", extra)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !bytes.Contains(body[:min(32, len(body))], []byte("ftyp")) {
			t.Fatalf("not a playable MP4: %d", resp.StatusCode)
		}
		if extra != "" && !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;") {
			t.Fatal("download disposition missing")
		}
	}
	resp := request("POST", "alice", "Workflow/test", `{"action":"delete"}`, "")
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, err = os.Stat(rec.dir); !os.IsNotExist(err) {
		t.Fatalf("recording directory still exists: %v", err)
	}
	if len(api.playwrightSessions("alice", "Workflow/test")) != 0 {
		t.Fatal("deleted recording still discoverable")
	}
	if request("GET", "alice", "Workflow/test", "", "").StatusCode != 404 {
		t.Fatal("deleted video still downloadable")
	}
}

func TestPlaywrightReplayCloseWhileRunning(t *testing.T) {
	api := &StreamingAPI{}
	s := &playwrightLiveSession{id: "pw-running", owner: "alice", workspace: "Workflow/test"}
	rec := api.newPlaywrightRecording(s)
	api.deletePlaywrightRecording(rec)
	rec.add([]byte("late frame"))
	rec.finish()
	time.Sleep(20 * time.Millisecond)
	if _, err := os.Stat(rec.dir); !os.IsNotExist(err) {
		t.Fatalf("late frame recreated deleted files: %v", err)
	}
	if state, _ := rec.status(); state != "deleted" {
		t.Fatal(state)
	}
}
