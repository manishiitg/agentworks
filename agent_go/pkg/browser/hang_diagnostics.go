package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Hang diagnostics: when a managed-browser command times out, the agent-browser
// daemon is usually stuck behind the page, so asking it for anything else hangs
// too. This talks to the session's Chrome directly over its DevTools port and
// saves what is needed to tell a busy page from a blocked one (server A 2026-09-25:
// every eval/get on the server A simulation page timed out at 30s, with no evidence
// either way).

const (
	hangProbeTimeout   = 2 * time.Second
	hangTraceDuration  = 3 * time.Second
	hangCPUSampleSpan  = time.Second
	hangTraceCallLimit = 5 * time.Second
	hangCaptureBudget  = 20 * time.Second
	hangCaptureMinGap  = time.Minute
	hangTraceByteLimit = 20 << 20
)

var (
	hangCaptureMu   sync.Mutex
	hangCaptureLast = map[string]time.Time{}
	// hangDiagRoot is where bundles are written; a var for tests.
	hangDiagRoot = filepath.Join(os.TempDir(), "agentworks-browser-diag")
)

// HangReport is one capture's findings.
type HangReport struct {
	Session   string           `json:"session"`
	Command   string           `json:"command"`
	At        time.Time        `json:"at"`
	ChromePID int              `json:"chrome_pid"`
	Processes []hangProcessCPU `json:"processes,omitempty"`
	Pages     []hangPageProbe  `json:"pages,omitempty"`
	TraceFile string           `json:"trace_file,omitempty"`
	Problems  []string         `json:"problems,omitempty"`
	Summary   string           `json:"summary"`
	Dir       string           `json:"dir"`
}

type hangProcessCPU struct {
	PID        int     `json:"pid"`
	Type       string  `json:"type"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSMB      int     `json:"rss_mb"`
}

type hangPageProbe struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	Responsive bool   `json:"responsive"`
	ProbeMS    int64  `json:"probe_ms"`
	ReadyState string `json:"ready_state,omitempty"`
	Screenshot string `json:"screenshot,omitempty"`
	Error      string `json:"error,omitempty"`
}

// captureHangDiagnostics saves a bundle for a timed-out command on session and
// returns a one-line summary. At most one capture per session per minute.
func captureHangDiagnostics(session, command string) string {
	hangCaptureMu.Lock()
	if last, ok := hangCaptureLast[session]; ok && time.Since(last) < hangCaptureMinGap {
		hangCaptureMu.Unlock()
		return ""
	}
	hangCaptureLast[session] = time.Now()
	hangCaptureMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), hangCaptureBudget)
	defer cancel()
	report := collectHangReport(ctx, session, command, sessionChromePID(session))
	if report.Dir != "" {
		if encoded, err := json.MarshalIndent(report, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(report.Dir, "report.json"), encoded, 0o600)
		}
	}
	log.Printf("[BROWSER_DIAG] hang capture for %q after %s timeout: %s (bundle: %s)", session, command, report.Summary, report.Dir)
	return report.Summary + " (debug bundle: " + report.Dir + ")"
}

func collectHangReport(ctx context.Context, session, command string, chromePID int) HangReport {
	report := HangReport{Session: session, Command: command, At: time.Now().UTC(), ChromePID: chromePID}
	dir := filepath.Join(hangDiagRoot, sanitizeDiagName(session)+"-"+report.At.Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o700); err == nil {
		report.Dir = dir
	}
	if chromePID <= 0 || !isProcessAlive(chromePID) {
		report.Summary = "Chrome for this session is not running"
		return report
	}
	report.Processes = sampleChromeCPU(chromePID)

	port, err := devToolsPortForPID(chromePID)
	if err != nil {
		report.Problems = append(report.Problems, "devtools port: "+err.Error())
		report.Summary = summarizeHang(report)
		return report
	}
	pages, err := listDevToolsPages(ctx, port)
	if err != nil {
		report.Problems = append(report.Problems, "list pages: "+err.Error())
	}
	for i, page := range pages {
		if i >= 3 {
			break
		}
		probe := probePage(ctx, page, report.Dir, i)
		report.Pages = append(report.Pages, probe)
	}
	if trace, err := captureTrace(ctx, port, report.Dir); err != nil {
		report.Problems = append(report.Problems, "trace: "+err.Error())
	} else {
		report.TraceFile = trace
	}
	report.Summary = summarizeHang(report)
	return report
}

// summarizeHang turns the probes into the line an agent reads: whether the
// page answered, and whether its renderer was busy.
func summarizeHang(report HangReport) string {
	renderer := 0.0
	for _, process := range report.Processes {
		if process.Type == "renderer" && process.CPUPercent > renderer {
			renderer = process.CPUPercent
		}
	}
	if len(report.Pages) == 0 {
		return fmt.Sprintf("could not reach the page over DevTools; busiest renderer %.0f%% CPU", renderer)
	}
	var stuck []string
	for _, page := range report.Pages {
		if !page.Responsive {
			stuck = append(stuck, page.URL)
		}
	}
	switch {
	case len(stuck) == 0:
		return fmt.Sprintf("the page answers directly (renderer %.0f%% CPU), so the browser controller, not the page, was stuck", renderer)
	case renderer >= 80:
		return fmt.Sprintf("page %s is not answering and its renderer is busy (%.0f%% CPU): the page's main thread is saturated; see trace.json", stuck[0], renderer)
	default:
		return fmt.Sprintf("page %s is not answering while its renderer is mostly idle (%.0f%% CPU): blocked, not busy (dialog, paused script or a synchronous wait); see trace.json", stuck[0], renderer)
	}
}

// sessionChromePID reads the Chrome PID recorded for session.
func sessionChromePID(session string) int {
	for _, dir := range sessionDirs() {
		if b, err := os.ReadFile(filepath.Join(dir, session+".chrome-pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

// sampleChromeCPU measures CPU per process in Chrome's tree over one second.
// Linux reads /proc; elsewhere ps's lifetime average is reported.
func sampleChromeCPU(chromePID int) []hangProcessCPU {
	pids := append([]int{chromePID}, descendantPIDs(chromePID)...)
	if runtime.GOOS != "linux" {
		return psCPU(pids)
	}
	before := map[int]uint64{}
	for _, pid := range pids {
		before[pid] = procCPUTicks(pid)
	}
	time.Sleep(hangCPUSampleSpan)
	ticksPerSecond := 100.0
	out := make([]hangProcessCPU, 0, len(pids))
	for _, pid := range pids {
		after := procCPUTicks(pid)
		if after == 0 {
			continue
		}
		used := float64(after-before[pid]) / ticksPerSecond / hangCPUSampleSpan.Seconds() * 100
		out = append(out, hangProcessCPU{PID: pid, Type: chromeProcessType(pid), CPUPercent: used, RSSMB: procRSSMB(pid)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPUPercent > out[j].CPUPercent })
	return out
}

func descendantPIDs(pid int) []int {
	var out []int
	queue := []int{pid}
	for len(queue) > 0 && len(out) < 64 {
		kids := childPIDs(queue[0])
		queue = queue[1:]
		out = append(out, kids...)
		queue = append(queue, kids...)
	}
	return out
}

func procCPUTicks(pid int) uint64 {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// Fields after the ")" of the command name: utime is 14th, stime 15th overall.
	text := string(raw)
	end := strings.LastIndex(text, ")")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 13 {
		return 0
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	return utime + stime
}

func procRSSMB(pid int) int {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.Atoi(fields[1])
				return kb / 1024
			}
		}
	}
	return 0
}

func processArgs(pid int) string {
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		return strings.ReplaceAll(string(raw), "\x00", " ")
	}
	out, _ := runCommand("ps", "-o", "args=", "-p", strconv.Itoa(pid))
	return strings.TrimSpace(out)
}

var chromeTypeFlag = regexp.MustCompile(`--type=([a-z-]+)`)

func chromeProcessType(pid int) string {
	if match := chromeTypeFlag.FindStringSubmatch(processArgs(pid)); match != nil {
		return match[1]
	}
	return "browser"
}

func psCPU(pids []int) []hangProcessCPU {
	out := make([]hangProcessCPU, 0, len(pids))
	for _, pid := range pids {
		line, err := runCommand("ps", "-o", "pcpu=,rss=", "-p", strconv.Itoa(pid))
		fields := strings.Fields(line)
		if err != nil || len(fields) < 2 {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[0], 64)
		rss, _ := strconv.Atoi(fields[1])
		out = append(out, hangProcessCPU{PID: pid, Type: chromeProcessType(pid), CPUPercent: cpu, RSSMB: rss / 1024})
	}
	return out
}

var userDataDirFlag = regexp.MustCompile(`--user-data-dir=(\S+)`)
var debuggingPortFlag = regexp.MustCompile(`--remote-debugging-port=(\d+)`)

// devToolsPortForPID finds Chrome's DevTools port: a fixed
// --remote-debugging-port, or for port 0 the DevToolsActivePort file Chrome
// writes into its user-data-dir.
func devToolsPortForPID(pid int) (int, error) {
	args := processArgs(pid)
	if match := debuggingPortFlag.FindStringSubmatch(args); match != nil && match[1] != "0" {
		return strconv.Atoi(match[1])
	}
	match := userDataDirFlag.FindStringSubmatch(args)
	if match == nil {
		return 0, fmt.Errorf("no --user-data-dir or fixed debugging port on Chrome %d", pid)
	}
	raw, err := os.ReadFile(filepath.Join(match[1], "DevToolsActivePort"))
	if err != nil {
		return 0, err
	}
	first := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)[0]
	return strconv.Atoi(strings.TrimSpace(first))
}

type devToolsTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func listDevToolsPages(ctx context.Context, port int) ([]devToolsTarget, error) {
	var targets []devToolsTarget
	if err := devToolsGetJSON(ctx, port, "/json/list", &targets); err != nil {
		return nil, err
	}
	pages := targets[:0]
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			pages = append(pages, target)
		}
	}
	return pages, nil
}

func devToolsGetJSON(ctx context.Context, port int, path string, into interface{}) error {
	reqCtx, cancel := context.WithTimeout(ctx, hangProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

// cdpConn is a minimal DevTools protocol client: numbered calls, and the
// events seen while waiting.
type cdpConn struct {
	ws     *websocket.Conn
	nextID int
}

func dialCDP(ctx context.Context, url string) (*cdpConn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: hangProbeTimeout}
	ws, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20)
	return &cdpConn{ws: ws}, nil
}

func (c *cdpConn) close() { _ = c.ws.Close() }

type cdpMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// call sends one command and waits up to timeout for its reply; onEvent sees
// every event read meanwhile.
func (c *cdpConn) call(method string, params interface{}, timeout time.Duration, onEvent func(cdpMessage)) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.ws.WriteJSON(map[string]interface{}{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		_ = c.ws.SetReadDeadline(deadline)
		var msg cdpMessage
		if err := c.ws.ReadJSON(&msg); err != nil {
			return nil, fmt.Errorf("%s: no reply within %s", method, timeout)
		}
		if msg.ID == id {
			if msg.Error != nil {
				return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
			}
			return msg.Result, nil
		}
		if msg.Method != "" && onEvent != nil {
			onEvent(msg)
		}
	}
}

// probePage asks the page's main thread for a trivial value (does it answer
// at all?), then for a screenshot.
func probePage(ctx context.Context, page devToolsTarget, dir string, index int) hangPageProbe {
	probe := hangPageProbe{URL: page.URL, Title: page.Title}
	conn, err := dialCDP(ctx, page.WebSocketDebuggerURL)
	if err != nil {
		probe.Error = "connect: " + err.Error()
		return probe
	}
	defer conn.close()
	started := time.Now()
	result, err := conn.call("Runtime.evaluate", map[string]interface{}{"expression": "document.readyState", "returnByValue": true}, hangProbeTimeout, nil)
	probe.ProbeMS = time.Since(started).Milliseconds()
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	probe.Responsive = true
	var evaluated struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(result, &evaluated) == nil {
		probe.ReadyState = evaluated.Result.Value
	}
	if dir == "" {
		return probe
	}
	shot, err := conn.call("Page.captureScreenshot", map[string]interface{}{"format": "png"}, hangProbeTimeout, nil)
	if err != nil {
		probe.Error = "screenshot: " + err.Error()
		return probe
	}
	var captured struct {
		Data string `json:"data"`
	}
	if json.Unmarshal(shot, &captured) == nil {
		if png, err := base64.StdEncoding.DecodeString(captured.Data); err == nil {
			name := fmt.Sprintf("page-%d.png", index)
			if os.WriteFile(filepath.Join(dir, name), png, 0o600) == nil {
				probe.Screenshot = name
			}
		}
	}
	return probe
}

// captureTrace records a short browser-wide performance trace: what every
// renderer's main thread was doing.
func captureTrace(ctx context.Context, port int, dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no bundle directory")
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := devToolsGetJSON(ctx, port, "/json/version", &version); err != nil {
		return "", err
	}
	conn, err := dialCDP(ctx, version.WebSocketDebuggerURL)
	if err != nil {
		return "", err
	}
	defer conn.close()
	var events []json.RawMessage
	size := 0
	collect := func(msg cdpMessage) {
		if msg.Method != "Tracing.dataCollected" || size > hangTraceByteLimit {
			return
		}
		var data struct {
			Value []json.RawMessage `json:"value"`
		}
		if json.Unmarshal(msg.Params, &data) == nil {
			for _, event := range data.Value {
				size += len(event)
				events = append(events, event)
			}
		}
	}
	categories := "devtools.timeline,disabled-by-default-devtools.timeline,v8.execute,blink,toplevel,disabled-by-default-v8.cpu_profiler,media"
	// Tracing.start waits for every renderer to acknowledge, so a renderer
	// whose main thread never yields can stall it: that is itself a finding.
	if _, err := conn.call("Tracing.start", map[string]interface{}{"categories": categories, "transferMode": "ReportEvents"}, hangTraceCallLimit, collect); err != nil {
		return "", fmt.Errorf("%w (a renderer did not acknowledge: its main thread is not yielding)", err)
	}
	time.Sleep(hangTraceDuration)
	if _, err := conn.call("Tracing.end", map[string]interface{}{}, hangTraceCallLimit, collect); err != nil {
		return "", err
	}
	// Remaining chunks arrive after the end reply, up to tracingComplete.
	deadline := time.Now().Add(hangProbeTimeout)
	for time.Now().Before(deadline) {
		_ = conn.ws.SetReadDeadline(deadline)
		var msg cdpMessage
		if conn.ws.ReadJSON(&msg) != nil || msg.Method == "Tracing.tracingComplete" {
			break
		}
		collect(msg)
	}
	encoded, err := json.Marshal(map[string]interface{}{"traceEvents": events})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "trace.json"), encoded, 0o600); err != nil {
		return "", err
	}
	return "trace.json", nil
}

var diagNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeDiagName(name string) string {
	name = diagNameUnsafe.ReplaceAllString(name, "_")
	if len(name) > 60 {
		name = name[:60]
	}
	return name
}
