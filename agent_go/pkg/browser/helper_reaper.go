package browser

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// Leftover agent-browser helper reaper (PLAT-662).
//
// agent-browser runs one daemon per --session (parent 1, env
// AGENT_BROWSER_DAEMON=1) and records the owning daemon's PID in
// <socket dir>/<session>.pid. Three kinds of leftovers piled up:
//   - orphans: a later start took the session over (new .pid, new socket) but
//     the old daemon kept running; it owns no socket any more;
//   - per-chat helpers (session-<hash>--browser) whose chat or extension
//     connection had ended;
//   - bookkeeping files (.config/.target/.sock/.pid ...) of sessions long gone.
//
// Only daemons of this user, in this process's mount namespace, are touched.
// The shared shared-cdp-<port> session is never closed, only its orphans.

const (
	helperReapInterval   = 10 * time.Minute
	helperStartGrace     = 2 * time.Minute // a new daemon writes its .pid shortly after start
	helperTermGrace      = 5 * time.Second
	helperStaleFileAge   = 24 * time.Hour
	helperCloseTimeout   = 8 * time.Second
	agentBrowserExecName = "agent-browser-"
)

var (
	perChatHelperSession = regexp.MustCompile(`^session-[a-f0-9]{16}--browser$`)
	helperFileExts       = []string{".chrome-pid", ".pid", ".sock", ".config", ".target", ".stream", ".engine", ".version"}

	helperReapMu        sync.Mutex
	helperLiveSessionFn func() []string
	listHelperDaemons   = listAgentBrowserDaemons
)

// helperDaemon is one running agent-browser daemon, as read from its process.
type helperDaemon struct {
	PID       int
	Age       time.Duration
	Session   string // AGENT_BROWSER_SESSION
	SocketDir string // AGENT_BROWSER_SOCKET_DIR ("" = agent-browser's default dirs)
	CDP       string // AGENT_BROWSER_CDP ("" = headless, owns its Chrome)
	Namespace string // AGENT_BROWSER_NAMESPACE
}

// SetHelperLiveSessions lets the server report the browser sessions of chats
// and runs that are still active. Until it is set, per-chat helpers are never
// closed (only orphans and stale files are reaped).
func SetHelperLiveSessions(fn func() []string) {
	helperReapMu.Lock()
	helperLiveSessionFn = fn
	helperReapMu.Unlock()
}

// StartHelperReaper reaps leftover agent-browser helpers now and every ~10 minutes.
func StartHelperReaper() {
	go func() {
		ReapLeftoverHelpers()
		ticker := time.NewTicker(helperReapInterval)
		defer ticker.Stop()
		for range ticker.C {
			ReapLeftoverHelpers()
		}
	}()
}

// ReapLeftoverHelpersAsync runs one reap in the background (used when a chat ends).
func ReapLeftoverHelpersAsync() { go ReapLeftoverHelpers() }

// ReapLeftoverHelpers runs one pass. It is a no-op when agent-browser is not
// installed, and overlapping calls skip instead of queueing.
func ReapLeftoverHelpers() {
	if _, err := exec.LookPath("agent-browser"); err != nil {
		return
	}
	if !helperReapMu.TryLock() {
		return
	}
	liveFn := helperLiveSessionFn
	defer helperReapMu.Unlock()

	daemons, err := listHelperDaemons()
	if err != nil {
		log.Printf("[BROWSER_HELPER_REAPER] could not list agent-browser daemons: %v", err)
		return
	}
	var live func(string) bool
	if liveFn != nil {
		names := map[string]bool{}
		for _, s := range liveFn() {
			names[s] = true
		}
		for _, s := range browserrelay.Default.LiveSessions() {
			names[s] = true
		}
		tracker := GetSessionTracker()
		tracker.mu.Lock()
		for s := range tracker.sessions {
			names[s] = true
		}
		tracker.mu.Unlock()
		live = func(s string) bool { return names[s] }
	}
	// A named instance shares the host with the main app: it may only end its
	// own (prefixed) chat helpers, never orphans or files that may be the app's.
	global := ShouldRunGlobalStartupCleanup()
	prefix := strings.TrimSpace(os.Getenv("AGENTWORKS_BROWSER_SESSION_PREFIX"))
	plan := planHelperReap(daemons, sessionDirs(), live, prefix, time.Now(), isProcessAlive)
	if !global {
		if prefix == "" {
			plan.Ended = nil
		}
		plan.Orphans, plan.StaleFiles = nil, nil
	}

	for _, d := range plan.Orphans {
		log.Printf("[BROWSER_HELPER_REAPER] orphan agent-browser daemon pid=%d session=%q age=%s (not the PID in any .pid file): terminating", d.PID, d.Session, d.Age.Round(time.Second))
		terminateHelperDaemon(d)
	}
	for _, d := range plan.Ended {
		log.Printf("[BROWSER_HELPER_REAPER] chat helper %q pid=%d cdp=%v has no live chat or connection: closing", d.Session, d.PID, d.CDP != "")
		closeEndedHelper(d)
	}
	for _, f := range plan.StaleFiles {
		if err := os.Remove(f); err == nil {
			log.Printf("[BROWSER_HELPER_REAPER] removed stale agent-browser file %s", f)
		}
	}
}

type helperReapPlan struct {
	Orphans    []helperDaemon
	Ended      []helperDaemon
	StaleFiles []string
}

// planHelperReap decides what to reap. live == nil means chat liveness is
// unknown, so no chat helper is closed.
func planHelperReap(daemons []helperDaemon, dirs []string, live func(string) bool, prefix string, now time.Time, pidAlive func(int) bool) helperReapPlan {
	var plan helperReapPlan
	seenDir := map[string]bool{}
	var scanDirs []string
	addDir := func(dir string) {
		if dir = filepath.Clean(dir); dir != "." && !seenDir[dir] {
			seenDir[dir] = true
			scanDirs = append(scanDirs, dir)
		}
	}
	for _, d := range dirs {
		addDir(d)
	}
	for _, d := range daemons {
		if d.SocketDir != "" {
			addDir(d.SocketDir)
		}
	}

	// Every PID recorded in any session's .pid file, and which dirs exist.
	recorded := map[int]bool{}
	existing := map[string]bool{}
	type entry struct {
		path, session string
		info          os.FileInfo
	}
	var files []entry
	for _, dir := range scanDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		existing[dir] = true
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			session := ""
			for _, ext := range helperFileExts {
				if strings.HasSuffix(name, ext) {
					session = strings.TrimSuffix(name, ext)
					break
				}
			}
			if session == "" {
				continue
			}
			path := filepath.Join(dir, name)
			if strings.HasSuffix(name, ".pid") && !strings.HasSuffix(name, ".chrome-pid") {
				if b, err := os.ReadFile(path); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
						recorded[pid] = true
					}
				}
			}
			if info, err := e.Info(); err == nil {
				files = append(files, entry{path: path, session: session, info: info})
			}
		}
	}

	liveDaemonSessions := map[string]bool{}
	for _, d := range daemons {
		liveDaemonSessions[d.Session] = true
		if d.Age < helperStartGrace {
			continue
		}
		if !recorded[d.PID] {
			// Only when the daemon names its socket dir (every AgentWorks spawn
			// does) and that dir is readable here; otherwise its .pid file may
			// live where this process does not look.
			if d.SocketDir != "" && existing[filepath.Clean(d.SocketDir)] {
				plan.Orphans = append(plan.Orphans, d)
			}
			continue
		}
		if live != nil && isPerChatHelper(d.Session, prefix) && !live(d.Session) {
			plan.Ended = append(plan.Ended, d)
		}
	}

	// A session whose .pid names a live process keeps all its files, even when
	// that daemon is not in the listing (another user or a sandbox namespace).
	pidLive := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f.path, ".pid") && !strings.HasSuffix(f.path, ".chrome-pid") {
			if b, err := os.ReadFile(f.path); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 && pidAlive(pid) {
					pidLive[filepath.Join(filepath.Dir(f.path), f.session)] = true
				}
			}
		}
	}
	uid := os.Getuid()
	for _, f := range files {
		if liveDaemonSessions[f.session] || pidLive[filepath.Join(filepath.Dir(f.path), f.session)] ||
			now.Sub(f.info.ModTime()) < helperStaleFileAge {
			continue
		}
		if st, ok := f.info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
			continue
		}
		plan.StaleFiles = append(plan.StaleFiles, f.path)
	}
	return plan
}

// isPerChatHelper reports whether session is a per-chat helper of this instance
// (SessionBrowserSessionNamespace or an extension relay connection).
func isPerChatHelper(session, prefix string) bool {
	if prefix != "" {
		if !strings.HasPrefix(session, prefix+"--") {
			return false
		}
		session = strings.TrimPrefix(session, prefix+"--")
	}
	return perChatHelperSession.MatchString(session)
}

func terminateHelperDaemon(d helperDaemon) {
	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		return
	}
	deadline := time.Now().Add(helperTermGrace)
	for time.Now().Before(deadline) {
		if !isProcessAlive(d.PID) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Re-check that the PID is still that daemon before SIGKILL (PID reuse).
	if current, err := listHelperDaemons(); err == nil {
		for _, c := range current {
			if c.PID == d.PID && c.Session == d.Session {
				_ = syscall.Kill(d.PID, syscall.SIGKILL)
				log.Printf("[BROWSER_HELPER_REAPER] daemon pid=%d ignored SIGTERM: killed", d.PID)
				return
			}
		}
	}
}

// closeEndedHelper ends a per-chat helper. A CDP-attached daemon is closed with
// `agent-browser --session X --cdp <endpoint> close`, which only disconnects
// (verified 2026-10-07: the Chrome and its pages stay open). A headless one
// owns its Chrome, so the full teardown runs.
func closeEndedHelper(d helperDaemon) {
	if d.CDP == "" {
		killSessionRuntimeFully(d.Session)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperCloseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "agent-browser", "--session", d.Session, "--cdp", d.CDP, "close", "--json")
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AGENT_BROWSER_SOCKET_DIR=") && !strings.HasPrefix(kv, "AGENT_BROWSER_NAMESPACE=") {
			env = append(env, kv)
		}
	}
	if d.SocketDir != "" {
		env = append(env, "AGENT_BROWSER_SOCKET_DIR="+d.SocketDir)
	}
	if d.Namespace != "" {
		env = append(env, "AGENT_BROWSER_NAMESPACE="+d.Namespace)
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		log.Printf("[BROWSER_HELPER_REAPER] close %q failed (%v): terminating its daemon", d.Session, err)
	}
	time.Sleep(500 * time.Millisecond)
	if isProcessAlive(d.PID) {
		terminateHelperDaemon(d) // CDP-attached: no Chrome of its own to leak
	}
	clearSessionTabScope(d.Session)
	removeSessionFiles(d.Session)
	browserconfig.RemoveEmptySessionSocketDirs(d.Session)
}

// listAgentBrowserDaemons lists this user's running agent-browser daemons.
func listAgentBrowserDaemons() ([]helperDaemon, error) {
	if runtime.GOOS == "linux" {
		return listAgentBrowserDaemonsProc()
	}
	return listAgentBrowserDaemonsPS()
}

func daemonFromEnv(pid int, age time.Duration, env map[string]string) (helperDaemon, bool) {
	if env["AGENT_BROWSER_DAEMON"] != "1" || strings.TrimSpace(env["AGENT_BROWSER_SESSION"]) == "" {
		return helperDaemon{}, false
	}
	return helperDaemon{
		PID: pid, Age: age,
		Session:   strings.TrimSpace(env["AGENT_BROWSER_SESSION"]),
		SocketDir: strings.TrimSpace(env["AGENT_BROWSER_SOCKET_DIR"]),
		CDP:       strings.TrimSpace(env["AGENT_BROWSER_CDP"]),
		Namespace: strings.TrimSpace(env["AGENT_BROWSER_NAMESPACE"]),
	}, true
}

// listAgentBrowserDaemonsProc reads /proc (Linux). Daemons in another mount
// namespace (a sandbox with its own /tmp) are skipped: their files are not
// where this process would look.
func listAgentBrowserDaemonsProc() ([]helperDaemon, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := os.Getuid()
	selfNS, _ := os.Readlink("/proc/self/ns/mnt")
	uptime := procUptime()
	var out []helperDaemon
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		if len(args) != 1 || !strings.HasPrefix(filepath.Base(args[0]), agentBrowserExecName) {
			continue
		}
		if ns, _ := os.Readlink(filepath.Join(dir, "ns", "mnt")); ns != selfNS {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "environ"))
		if err != nil {
			continue
		}
		env := map[string]string{}
		for _, kv := range strings.Split(string(raw), "\x00") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		if d, ok := daemonFromEnv(pid, procAge(dir, uptime), env); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func procUptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// procAge reads the start time (field 22, clock ticks; USER_HZ is 100 on Linux).
func procAge(dir string, uptime float64) time.Duration {
	b, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil || uptime == 0 {
		return 0
	}
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 {
		s = s[i+1:]
	}
	f := strings.Fields(s) // f[0] is field 3 (state)
	if len(f) < 20 {
		return 0
	}
	ticks, err := strconv.ParseFloat(f[19], 64)
	if err != nil {
		return 0
	}
	age := uptime - ticks/100
	if age < 0 {
		return 0
	}
	return time.Duration(age * float64(time.Second))
}

// listAgentBrowserDaemonsPS uses ps (macOS); the environment comes from `ps eww`.
func listAgentBrowserDaemonsPS() ([]helperDaemon, error) {
	out, err := runCommand("ps", "-A", "-o", "pid=,uid=,etime=,args=")
	if err != nil {
		return nil, err
	}
	uid := os.Getuid()
	var daemons []helperDaemon
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// A daemon runs as the bare binary with no arguments.
		if len(f) != 4 || !strings.HasPrefix(filepath.Base(f[3]), agentBrowserExecName) {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		puid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil || puid != uid {
			continue
		}
		envOut, err := runCommand("ps", "eww", "-o", "command=", "-p", strconv.Itoa(pid))
		if err != nil {
			continue
		}
		if d, ok := daemonFromEnv(pid, parseEtime(f[2]), parsePSEnv(envOut)); ok {
			daemons = append(daemons, d)
		}
	}
	return daemons, nil
}

var psEnvKey = regexp.MustCompile(`(?:^|\s)([A-Za-z_][A-Za-z0-9_]*)=`)

// parsePSEnv splits `ps eww` output ("<command> K=V K=V ...") into variables.
// A value runs until the next " KEY=", so values with spaces survive.
func parsePSEnv(s string) map[string]string {
	s = strings.TrimSpace(s)
	env := map[string]string{}
	idx := psEnvKey.FindAllStringSubmatchIndex(s, -1)
	for i, m := range idx {
		end := len(s)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		env[s[m[2]:m[3]]] = strings.TrimSpace(s[m[1]:end])
	}
	return env
}

// parseEtime parses ps etime ("[[dd-]hh:]mm:ss").
func parseEtime(s string) time.Duration {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		days, _ = strconv.Atoi(d)
		s = rest
	}
	parts := strings.Split(s, ":")
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		secs = secs*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second
}
