package voicestt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	helperStartTimeout  = 15 * time.Second
	helperStatusTimeout = 2 * time.Second
	helperControlTimeout = 5 * time.Second
)

type helperProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	port  int
	token string
	done  chan struct{}
}

func (p *helperProcess) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.port)
}

func (p *helperProcess) wsURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d/stream", p.port)
}

func (p *helperProcess) stop() {
	if p == nil {
		return
	}
	// Closing stdin signals the helper's stdin-EOF watchdog to shut down cleanly.
	_ = p.stdin.Close()
	select {
	case <-p.done:
		return
	case <-time.After(2 * time.Second):
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
}

// HelperClient implements Service by lazily spawning a standalone voice-helper
// binary and proxying status, warm, unload, transcribe, and dictation WebSocket
// traffic to it over authenticated loopback HTTP/WS.
//
// Because the helper is a separate binary, agent-server never links sherpa-onnx
// or onnxruntime directly, allowing one agent-server binary to ship for all
// macOS versions while still running voice on supported Macs and reclaiming the
// full ~1 GB ONNX resident set when Unload exits the helper process.
type HelperClient struct {
	binPath  string
	extraArg []string
	modelDir string
	http     *http.Client

	mu        sync.Mutex
	proc      *helperProcess
	starting  bool
	startDone chan struct{}
	warming   bool
	lastErr   error
	closed    bool

	active atomic.Int32
}

// NewHelperClient creates a lazy Service backed by the executable at binPath.
func NewHelperClient(binPath, modelDir string, extraArgs ...string) *HelperClient {
	return &HelperClient{
		binPath:  binPath,
		extraArg: append([]string(nil), extraArgs...),
		modelDir: modelDir,
		http:     &http.Client{},
	}
}

// Available reports true because HelperClient is only constructed when a
// compatible helper binary is present.
func (*HelperClient) Available() bool { return true }

// Status returns a non-blocking snapshot of the voice engine state. When the
// helper has not been spawned yet, it answers locally from disk without
// starting a subprocess.
func (c *HelperClient) Status() Status {
	c.mu.Lock()
	proc := c.proc
	warming := c.warming || c.starting
	lastErr := c.lastErr
	c.mu.Unlock()

	installed := ModelInstalled(c.modelDir)
	fallback := Status{
		Available:     true,
		Installed:     installed,
		Downloading:   warming && !installed,
		Loading:       warming && installed,
		Ready:         false,
		ActiveStreams: int(c.active.Load()),
		ModelDir:      c.modelDir,
		SizeMB:        ModelSizeMB,
	}
	if lastErr != nil {
		fallback.Error = lastErr.Error()
	}
	if proc == nil {
		return fallback
	}

	ctx, cancel := context.WithTimeout(context.Background(), helperStatusTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proc.baseURL()+"/status", nil)
	if err != nil {
		return fallback
	}
	req.Header.Set(helperTokenHeader, proc.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback
	}
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return fallback
	}
	if st.ModelDir == "" {
		st.ModelDir = c.modelDir
	}
	if st.SizeMB == 0 {
		st.SizeMB = ModelSizeMB
	}
	return st
}

// Warm spawns the helper (if needed) and asks it to begin downloading/loading
// the speech model in the background. Returns immediately so server startup
// and POST /api/voice/warm are never blocked.
func (c *HelperClient) Warm() {
	c.mu.Lock()
	if c.closed || c.warming {
		c.mu.Unlock()
		return
	}
	c.warming = true
	c.lastErr = nil
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			c.warming = false
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), helperStartTimeout+helperControlTimeout)
		defer cancel()
		proc, err := c.ensureStarted(ctx)
		if err != nil {
			log.Printf("[VOICE] helper warm start failed: %v", err)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, proc.baseURL()+"/warm", nil)
		if err != nil {
			return
		}
		req.Header.Set(helperTokenHeader, proc.token)
		resp, err := c.http.Do(req)
		if err != nil {
			log.Printf("[VOICE] helper /warm failed: %v", err)
			c.mu.Lock()
			c.lastErr = err
			c.mu.Unlock()
			return
		}
		_ = resp.Body.Close()
	}()
}

// Unload asks the helper to release its loaded ONNX model and, if released and
// idle, stops the helper process so the OS reclaims the full ~1 GB RSS.
func (c *HelperClient) Unload() bool {
	if c.active.Load() > 0 {
		return false
	}
	c.mu.Lock()
	proc := c.proc
	if proc == nil || c.warming || c.starting {
		c.mu.Unlock()
		return false
	}
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), helperControlTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proc.baseURL()+"/unload", nil)
	if err != nil {
		return false
	}
	req.Header.Set(helperTokenHeader, proc.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out helperUnloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	if !out.Released {
		return false
	}

	c.mu.Lock()
	var toStop *helperProcess
	if c.proc == proc && !c.warming && !c.starting && c.active.Load() == 0 {
		c.proc = nil
		toStop = proc
	}
	c.mu.Unlock()
	if toStop != nil {
		toStop.stop()
	}
	return true
}

// Transcribe forwards a decoded 16kHz mono float32 clip to the helper.
func (c *HelperClient) Transcribe(ctx context.Context, samples []float32) (string, error) {
	if len(samples) == 0 {
		return "", nil
	}
	c.active.Add(1)
	defer c.active.Add(-1)

	proc, err := c.ensureStarted(ctx)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proc.baseURL()+"/transcribe", bytes.NewReader(encodeFloat32LE(samples)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(helperTokenHeader, proc.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out helperTranscribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode voice helper response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return "", errors.New(out.Error)
		}
		return "", fmt.Errorf("voice helper transcribe failed with status %d", resp.StatusCode)
	}
	return out.Text, nil
}

// ServeStream proxies an authorized dictation WebSocket to the helper process.
func (c *HelperClient) ServeStream(upgrader *websocket.Upgrader, w http.ResponseWriter, r *http.Request) {
	c.active.Add(1)
	defer c.active.Add(-1)

	proc, err := c.ensureStarted(r.Context())
	if err != nil {
		log.Printf("[VOICE] session refused: helper start failed: %v", err)
		http.Error(w, `{"error":"voice engine unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	dialer := websocket.Dialer{
		ReadBufferSize:  4 * 1024,
		WriteBufferSize: 32 * 1024,
	}
	headers := http.Header{helperTokenHeader: []string{proc.token}}
	upstream, resp, err := dialer.DialContext(r.Context(), proc.wsURL(), headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		log.Printf("[VOICE] session refused: helper stream dial failed: %v", err)
		http.Error(w, `{"error":"voice engine unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	defer upstream.Close()

	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[VOICE] upgrade failed: %v", err)
		return
	}
	defer client.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Helper (binary PCM16 audio frames and JSON control messages).
	go func() {
		defer wg.Done()
		for {
			msgType, data, readErr := client.ReadMessage()
			if readErr != nil {
				_ = upstream.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(time.Second),
				)
				_ = upstream.Close()
				return
			}
			if writeErr := upstream.WriteMessage(msgType, data); writeErr != nil {
				return
			}
		}
	}()

	// Helper -> Client (JSON partial/final/error transcript frames).
	go func() {
		defer wg.Done()
		for {
			msgType, data, readErr := upstream.ReadMessage()
			if readErr != nil {
				_ = client.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(time.Second),
				)
				_ = client.Close()
				return
			}
			if writeErr := client.WriteMessage(msgType, data); writeErr != nil {
				return
			}
		}
	}()

	wg.Wait()
}

// Close stops any running helper process and prevents further spawns.
func (c *HelperClient) Close() error {
	c.mu.Lock()
	c.closed = true
	proc := c.proc
	c.proc = nil
	c.mu.Unlock()
	if proc != nil {
		proc.stop()
	}
	return nil
}

func (c *HelperClient) ensureStarted(ctx context.Context) (*helperProcess, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, errors.New("voice helper client closed")
		}
		if c.proc != nil {
			proc := c.proc
			c.mu.Unlock()
			return proc, nil
		}
		if c.starting {
			done := c.startDone
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		c.starting = true
		c.startDone = make(chan struct{})
		c.lastErr = nil
		c.mu.Unlock()

		proc, err := c.spawn(ctx)

		c.mu.Lock()
		c.starting = false
		if err != nil {
			c.lastErr = err
		} else if c.closed {
			// Closed concurrently while spawning: tear down the new process.
			close(c.startDone)
			c.mu.Unlock()
			proc.stop()
			return nil, errors.New("voice helper client closed")
		} else {
			c.proc = proc
			c.lastErr = nil
		}
		close(c.startDone)
		c.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return proc, nil
	}
}

func (c *HelperClient) spawn(ctx context.Context) (*helperProcess, error) {
	token, err := randomHexToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate helper token: %w", err)
	}

	args := make([]string, 0, len(c.extraArg)+3)
	args = append(args, c.extraArg...)
	args = append(args, "--stdin-auth", "--model-dir", c.modelDir)

	cmd := exec.Command(c.binPath, args...) //nolint:gosec // G204: path comes from trusted packaged runtime config.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("helper stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("helper stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("helper stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start voice helper: %w", err)
	}

	// Forward helper logs to agent-server's logger so diagnostics land in agent.log.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				log.Printf("[VOICE-HELPER] %s", line)
			}
		}
	}()

	if _, err := io.WriteString(stdin, token+"\n"); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("write voice helper auth token: %w", err)
	}

	readyCh := make(chan helperReadyMessage, 1)
	readyErrCh := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil {
			readyErrCh <- fmt.Errorf("read voice helper ready line: %w", readErr)
			return
		}
		var msg helperReadyMessage
		if unmarshalErr := json.Unmarshal(bytes.TrimSpace(line), &msg); unmarshalErr != nil {
			readyErrCh <- fmt.Errorf("parse voice helper ready line %q: %w", strings.TrimSpace(string(line)), unmarshalErr)
			return
		}
		if msg.Port <= 0 || msg.Port > 65535 {
			readyErrCh <- fmt.Errorf("invalid voice helper port %d", msg.Port)
			return
		}
		readyCh <- msg
		_, _ = io.Copy(io.Discard, reader)
	}()

	startTimer := time.NewTimer(helperStartTimeout)
	defer startTimer.Stop()

	var ready helperReadyMessage
	select {
	case ready = <-readyCh:
	case readyErr := <-readyErrCh:
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, readyErr
	case <-startTimer.C:
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("voice helper did not become ready within %s", helperStartTimeout)
	case <-ctx.Done():
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, ctx.Err()
	}

	proc := &helperProcess{
		cmd:   cmd,
		stdin: stdin,
		port:  ready.Port,
		token: token,
		done:  make(chan struct{}),
	}

	go func() {
		waitErr := cmd.Wait()
		close(proc.done)
		c.mu.Lock()
		if c.proc == proc {
			c.proc = nil
			if waitErr != nil && !c.closed {
				c.lastErr = fmt.Errorf("voice helper exited: %w", waitErr)
				log.Printf("[VOICE] helper exited unexpectedly: %v", waitErr)
			}
		}
		c.mu.Unlock()
	}()

	log.Printf("[VOICE] started voice-helper on 127.0.0.1:%d (pid=%d)", proc.port, cmd.Process.Pid)
	return proc, nil
}

func randomHexToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
