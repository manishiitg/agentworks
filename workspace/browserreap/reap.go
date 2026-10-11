// Package browserreap finds and ends leftover agent-browser helpers (PLAT-662).
//
// agent-browser runs one daemon per --session (parent 1, env
// AGENT_BROWSER_DAEMON=1) and records the owning daemon's PID in
// <socket dir>/<session>.pid. Leftovers pile up:
//   - orphans: a later start took the session over (new .pid, new socket) but
//     the old daemon kept running; nothing can reach it any more;
//   - per-chat helpers whose chat or extension connection had ended (only the
//     server knows which chats are live, so it decides those itself);
//   - bookkeeping files (.config/.target/.sock/.pid ...) of sessions long gone.
//
// Only processes and files of the calling user are ever touched. The server
// runs this for its own account; each slot account runs ReapOwnLeftovers for
// itself through its Landlock launcher (reap-browser-helpers), because the
// server can neither see nor signal another account's processes.
package browserreap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

const (
	// StartGrace: a new daemon writes its .pid shortly after start.
	StartGrace = 2 * time.Minute
	// StaleFileAge: bookkeeping files of a session with no live daemon are kept this long.
	StaleFileAge = 24 * time.Hour
	termGrace    = 5 * time.Second
	execPrefix   = "agent-browser-"
	// SocketRoot is agent-browser's shared socket folder (browserconfig.SocketRoot).
	SocketRoot = "/tmp/.agent-browser"
)

var (
	perChatHelperSession = regexp.MustCompile(`^session-[a-f0-9]{16}--browser$`)
	fileExts             = []string{".chrome-pid", ".pid", ".sock", ".config", ".target", ".stream", ".engine", ".version"}
)

// Daemon is one running agent-browser daemon, as read from its process.
type Daemon struct {
	PID       int
	Age       time.Duration
	Session   string // AGENT_BROWSER_SESSION
	SocketDir string // AGENT_BROWSER_SOCKET_DIR ("" = agent-browser's default dirs)
	CDP       string // AGENT_BROWSER_CDP ("" = headless, owns its Chrome)
	Namespace string // AGENT_BROWSER_NAMESPACE
}

// Plan is what one pass would reap.
type Plan struct {
	Orphans    []Daemon
	Ended      []Daemon
	StaleFiles []string
}

// PlanReap decides what to reap. live == nil means chat liveness is unknown,
// so no chat helper is closed (only orphans and stale files).
func PlanReap(daemons []Daemon, dirs []string, live func(string) bool, prefix string, now time.Time, pidAlive func(int) bool) Plan {
	var plan Plan
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
			for _, ext := range fileExts {
				if strings.HasSuffix(name, ext) {
					session = strings.TrimSuffix(name, ext)
					break
				}
			}
			if session == "" {
				continue
			}
			path := filepath.Join(dir, name)
			if isPidFile(name) {
				if pid := readPid(path); pid > 0 {
					recorded[pid] = true
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
		if d.Age < StartGrace {
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
		if live != nil && IsPerChatHelper(d.Session, prefix) && !live(d.Session) {
			plan.Ended = append(plan.Ended, d)
		}
	}

	// A session whose .pid names a live process keeps all its files, even when
	// that daemon is not in the listing (another user or a sandbox namespace).
	pidLive := map[string]bool{}
	for _, f := range files {
		if isPidFile(filepath.Base(f.path)) {
			if pid := readPid(f.path); pid > 0 && pidAlive(pid) {
				pidLive[filepath.Join(filepath.Dir(f.path), f.session)] = true
			}
		}
	}
	uid := os.Getuid()
	for _, f := range files {
		if liveDaemonSessions[f.session] || pidLive[filepath.Join(filepath.Dir(f.path), f.session)] ||
			now.Sub(f.info.ModTime()) < StaleFileAge {
			continue
		}
		if st, ok := f.info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
			continue
		}
		plan.StaleFiles = append(plan.StaleFiles, f.path)
	}
	return plan
}

func isPidFile(name string) bool {
	return strings.HasSuffix(name, ".pid") && !strings.HasSuffix(name, ".chrome-pid")
}

func readPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}

// IsPerChatHelper reports whether session is a per-chat helper of an instance
// with this session prefix (a chat browser namespace or an extension relay).
func IsPerChatHelper(session, prefix string) bool {
	if prefix != "" {
		if !strings.HasPrefix(session, prefix+"--") {
			return false
		}
		session = strings.TrimPrefix(session, prefix+"--")
	}
	return perChatHelperSession.MatchString(session)
}

// ProcessAlive reports whether pid is a live process. A process of another
// account (EPERM) is alive: its session's files are not this account's to judge.
func ProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Terminate ends a daemon: SIGTERM, then SIGKILL if it is still the same
// daemon after a short grace.
func Terminate(d Daemon) {
	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		return
	}
	deadline := time.Now().Add(termGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(d.PID, 0) != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Re-check that the PID is still that daemon before SIGKILL (PID reuse).
	if current, err := ListDaemons(); err == nil {
		for _, c := range current {
			if c.PID == d.PID && c.Session == d.Session {
				_ = syscall.Kill(d.PID, syscall.SIGKILL)
				return
			}
		}
	}
}

// DefaultDirs are the socket folders agent-browser uses for this account
// without a configured namespace: ~/.agent-browser, /tmp/.agent-browser and
// the per-owner folders under it.
func DefaultDirs() []string {
	var dirs []string
	if home, _ := os.UserHomeDir(); home != "" && filepath.Join(home, ".agent-browser") != SocketRoot {
		dirs = append(dirs, filepath.Join(home, ".agent-browser"))
	}
	dirs = append(dirs, SocketRoot)
	dirs = append(dirs, browserconfig.ManagedSocketDirs()...)
	return dirs
}

// ReapOwnLeftovers is the pass a slot account runs for itself: it ends its own
// orphaned daemons and removes its own stale files. It never closes a daemon a
// .pid file still names (a live job may be using it). It returns one line per
// action taken.
func ReapOwnLeftovers(dirs []string) ([]string, error) {
	daemons, err := ListDaemons()
	if err != nil {
		return nil, err
	}
	plan := PlanReap(daemons, dirs, nil, "", time.Now(), ProcessAlive)
	var done []string
	for _, d := range plan.Orphans {
		Terminate(d)
		done = append(done, fmt.Sprintf("terminated orphan agent-browser daemon pid=%d session=%q age=%s", d.PID, d.Session, d.Age.Round(time.Second)))
	}
	for _, f := range plan.StaleFiles {
		if err := os.Remove(f); err == nil {
			done = append(done, "removed stale agent-browser file "+f)
		}
	}
	return done, nil
}

// ListDaemons lists this user's running agent-browser daemons.
func ListDaemons() ([]Daemon, error) {
	if runtime.GOOS == "linux" {
		return listDaemonsProc()
	}
	return listDaemonsPS()
}

func daemonFromEnv(pid int, age time.Duration, env map[string]string) (Daemon, bool) {
	if env["AGENT_BROWSER_DAEMON"] != "1" || strings.TrimSpace(env["AGENT_BROWSER_SESSION"]) == "" {
		return Daemon{}, false
	}
	return Daemon{
		PID: pid, Age: age,
		Session:   strings.TrimSpace(env["AGENT_BROWSER_SESSION"]),
		SocketDir: strings.TrimSpace(env["AGENT_BROWSER_SOCKET_DIR"]),
		CDP:       strings.TrimSpace(env["AGENT_BROWSER_CDP"]),
		Namespace: strings.TrimSpace(env["AGENT_BROWSER_NAMESPACE"]),
	}, true
}

// listDaemonsProc reads /proc (Linux). A daemon in another mount namespace (a
// sandboxed command with its own /tmp) is listed only when its socket folder is
// the very folder this process sees at that path (the shared /tmp/.agent-browser
// bound into the sandbox): only then are its .pid and socket files the ones
// this process reads. Otherwise it is skipped.
func listDaemonsProc() ([]Daemon, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := os.Getuid()
	selfNS, _ := os.Readlink("/proc/self/ns/mnt")
	uptime := procUptime()
	var out []Daemon
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
		if len(args) != 1 || !strings.HasPrefix(filepath.Base(args[0]), execPrefix) {
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
		d, ok := daemonFromEnv(pid, procAge(dir, uptime), env)
		if !ok {
			continue
		}
		if ns, _ := os.Readlink(filepath.Join(dir, "ns", "mnt")); ns != selfNS {
			if !filepath.IsAbs(d.SocketDir) || !sameDir(filepath.Join(dir, "root", d.SocketDir), d.SocketDir) {
				continue
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// sameDir reports whether a and b are the same existing folder.
func sameDir(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil || !ia.IsDir() {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil || !ib.IsDir() {
		return false
	}
	return os.SameFile(ia, ib)
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

// listDaemonsPS uses ps (macOS); the environment comes from `ps eww`.
func listDaemonsPS() ([]Daemon, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,uid=,etime=,args=").Output()
	if err != nil {
		return nil, err
	}
	uid := os.Getuid()
	var daemons []Daemon
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		// A daemon runs as the bare binary with no arguments.
		if len(f) != 4 || !strings.HasPrefix(filepath.Base(f[3]), execPrefix) {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		puid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil || puid != uid {
			continue
		}
		envOut, err := exec.Command("ps", "eww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			continue
		}
		if d, ok := daemonFromEnv(pid, parseEtime(f[2]), parsePSEnv(string(envOut))); ok {
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
