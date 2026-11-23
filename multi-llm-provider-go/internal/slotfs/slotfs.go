// Package slotfs lets the coding-CLI launch code tell that a launch belongs to a user's own Linux
// account (a "slot", provisioned by the builder's provision-slots.sh) and prepare it accordingly.
//
// A launch is a slot launch when its working folder lies in a slot's state folder,
// <state root>/<slot>/..., as set by the agent server. Nothing else changes for hosts without
// slots: with no allow-list file every function here reduces to what the code did before
// (os.TempDir, owner-only modes, no sudo). There is no separate switch: one system everywhere
// (owner 2026-10-30, PLAT-872-84).
//
// The roots come from the root-owned allow-list that slotctl also reads, so the platform cannot point
// them elsewhere. The request format in WrapCmd mirrors the builder's workspace/slots.ExecRequest.
package slotfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	// ConfigPath is the root-owned allow-list written by provision-slots.sh.
	ConfigPath = "/usr/local/libexec/agentworks/slotctl.json"
	// EnvConfig overrides ConfigPath (tests).
	EnvConfig = "AGENTWORKS_SLOTCTL_CONFIG"

	sudoPath    = "/usr/bin/sudo"
	slotctlPath = "/usr/local/libexec/agentworks/slotctl"
	// EnvSlotctl overrides slotctlPath: a product that has its own slot accounts on a shared host.
	EnvSlotctl = "AGENTWORKS_SLOTCTL"
	// EnvPrefix names the slot accounts of this product (default "slot": slot01, slot02, ...).
	EnvPrefix = "AGENTWORKS_SLOT_PREFIX"
)

// slotctl is the root-owned launcher this product's service account may run as a slot.
func slotctl() string {
	if override := strings.TrimSpace(os.Getenv(EnvSlotctl)); override != "" {
		return override
	}
	return slotctlPath
}

var (
	nameMu    sync.Mutex
	nameCache = map[string]*regexp.Regexp{}
)

// slotNameMatches reports whether name is one of this product's slot accounts: the product's prefix (the
// config's slot_prefix, else AGENTWORKS_SLOT_PREFIX, else "slot") followed by two or three digits.
func slotNameMatches(name string) bool {
	prefix := strings.TrimSpace(os.Getenv(EnvPrefix))
	if cfg, ok := loadConfigUnchecked(); ok && cfg.SlotPrefix != "" {
		prefix = cfg.SlotPrefix
	}
	if prefix == "" {
		prefix = "slot"
	}
	nameMu.Lock()
	re, ok := nameCache[prefix]
	if !ok {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "[0-9]{2,3}$")
		nameCache[prefix] = re
	}
	nameMu.Unlock()
	return re.MatchString(name)
}

type config struct {
	SlotPrefix string `json:"slot_prefix"`
	// SlotDocker says every slot has its own rootless Docker (provision-slots.sh docker): a command run as the
	// slot gets DOCKER_HOST pointing at it instead of the platform account's socket, which a slot cannot reach.
	SlotDocker    bool   `json:"slot_docker"`
	SlotRunRoot   string `json:"slot_run_root"`
	SlotStateRoot string `json:"slot_state_root"`
	DocsRoot      string `json:"docs_root"`
	SlotTable     string `json:"slot_table"`
}

func loadConfig() (config, bool) {
	return loadConfigUnchecked()
}

// loadConfigUnchecked reads the allow-list whether or not CLI launches as slots are switched on.
func loadConfigUnchecked() (config, bool) {
	path := ConfigPath
	if override := strings.TrimSpace(os.Getenv(EnvConfig)); override != "" {
		path = override
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, false
	}
	var cfg config
	if json.Unmarshal(data, &cfg) != nil || cfg.SlotRunRoot == "" || cfg.SlotStateRoot == "" {
		return config{}, false
	}
	return cfg, true
}

// ErrLaunchBlocked is what a launch gets when the application named a user and a slot for it and the host's slot
// table does not confirm them (PLAT-451). Running such a launch as the app account would silently drop the
// isolation the application asked for, so it is refused: callers must stop the launch, never treat it as "no slot".
var ErrLaunchBlocked = errors.New("slot launch blocked")

// LaunchBlockedError says what the application declared and what the host says.
type LaunchBlockedError struct {
	Path, User, DeclaredSlot, TableSlot string
}

func (e *LaunchBlockedError) Error() string {
	if e.TableSlot == "" {
		return fmt.Sprintf("[SLOT_EXPLICIT_MISMATCH] launch refused for %s: the application named slot %q for user %s and the host has no confirmed slot for that user", e.Path, e.DeclaredSlot, e.User)
	}
	return fmt.Sprintf("[SLOT_EXPLICIT_MISMATCH] launch refused for %s: the application named slot %q for user %s but the host's slot table says %q", e.Path, e.DeclaredSlot, e.User, e.TableSlot)
}

// Is makes errors.Is(err, ErrLaunchBlocked) true for a *LaunchBlockedError.
func (e *LaunchBlockedError) Is(target error) bool { return target == ErrLaunchBlocked }

// SlotOf returns the slot a path belongs to (a path inside <state root>/<slot> or <run root>/<slot>). A launch
// the policy blocks (see SlotOfChecked) reports no slot here: every launch path must call CheckLaunch (or
// SlotOfChecked) first, which the launch functions in this module do, so that "blocked" is never run as "no slot".
func SlotOf(path string) (slot string, ok bool) {
	slot, ok, err := SlotOfChecked(path)
	if err != nil {
		log.Print(err.Error())
		return "", false
	}
	return slot, ok
}

// CheckLaunch returns a *LaunchBlockedError (errors.Is ErrLaunchBlocked) when a launch in path must not run, nil
// otherwise (including every launch that is simply not a slot's).
func CheckLaunch(path string) error {
	_, _, err := SlotOfChecked(path)
	return err
}

// SlotOfChecked is SlotOf with the refusal made explicit: err is non-nil when the application declared a user and
// slot for this launch that the host's table does not confirm and the canary allows that user.
func SlotOfChecked(path string) (slot string, ok bool, err error) {
	cfg, on := loadConfig()
	if !on || strings.TrimSpace(path) == "" {
		return "", false, nil
	}
	clean := filepath.Clean(path)
	for _, root := range []string{cfg.SlotStateRoot, cfg.SlotRunRoot} {
		prefix := filepath.Clean(root) + string(filepath.Separator)
		if strings.HasPrefix(clean, prefix) {
			first := strings.SplitN(strings.TrimPrefix(clean, prefix), string(filepath.Separator), 2)[0]
			if slotNameMatches(first) {
				return first, true, nil
			}
		}
	}
	// The application's explicit decision for this launch (PLAT-442): the owner's slot, none, or a refusal.
	if declared, found := llmtypes.DeclaredRunAs(path); found {
		slot, ok, err := explicitSlot(cfg, declared)
		if err != nil {
			err.Path = path
			return "", false, err
		}
		return slot, ok, nil
	}
	// Fallback while launches that do not declare one remain: a user's own tree,
	// <docs root>/_users/<user id>/..., belongs to the slot that user holds. Logged, so any remaining
	// path-guessing is visible.
	if slot, ok := pathInferredSlot(cfg, clean); ok {
		logFallback(clean, slot)
		return slot, true, nil
	}
	return "", false, nil
}

// explicitSlot is the slot the application named, checked against the host's slot table. The table is the host's
// truth. A declared app account, or a user the canary does not cover, or a user named without a slot that the host
// does not hold one for, is "no slot". When the application named a slot for a covered user and the table disagrees
// or has none, the launch is refused (PLAT-451) instead of falling back to the app account.
func explicitSlot(cfg config, declared llmtypes.RunAs) (string, bool, *LaunchBlockedError) {
	user := strings.TrimSpace(declared.User)
	if user == "" {
		return "", false, nil // declared: the app account
	}
	slot := ""
	if cfg.SlotTable != "" {
		slot = slotOfUser(cfg.SlotTable, user)
		if declared.Slot != "" && slot != declared.Slot {
			return "", false, &LaunchBlockedError{User: user, DeclaredSlot: declared.Slot, TableSlot: slot}
		}
	} else if declared.Slot != "" {
		if !slotNameMatches(declared.Slot) {
			return "", false, &LaunchBlockedError{User: user, DeclaredSlot: declared.Slot}
		}
		slot = declared.Slot
	}
	if slot == "" {
		return "", false, nil
	}
	return slot, true, nil
}

// pathInferredSlot is the old folder rule: the slot of the user whose tree <docs root>/_users/<id>/ holds path.
func pathInferredSlot(cfg config, clean string) (string, bool) {
	if cfg.DocsRoot == "" || cfg.SlotTable == "" {
		return "", false
	}
	prefix := filepath.Join(filepath.Clean(cfg.DocsRoot), "_users") + string(filepath.Separator)
	if !strings.HasPrefix(clean, prefix) {
		return "", false
	}
	userID := strings.SplitN(strings.TrimPrefix(clean, prefix), string(filepath.Separator), 2)[0]
	if slot := slotOfUser(cfg.SlotTable, userID); slot != "" {
		return slot, true
	}
	return "", false
}

var fallbackLogged sync.Map

// logFallback says once per folder that the slot came from the path.
func logFallback(path, slot string) {
	if _, seen := fallbackLogged.LoadOrStore(path+"\x00"+slot, true); !seen {
		log.Printf("[SLOT_FALLBACK] path-inferred slot %s for %s: the application did not declare a run-as identity for this launch", slot, path)
	}
}

func slotOfUser(tablePath, userID string) string {
	data, err := os.ReadFile(tablePath)
	if err != nil || userID == "" {
		return ""
	}
	var table struct {
		Slots map[string]string `json:"slots"`
	}
	if json.Unmarshal(data, &table) != nil {
		return ""
	}
	for slot, holder := range table.Slots {
		if holder == userID && slotNameMatches(slot) {
			return slot
		}
	}
	return ""
}

// RunDir is a slot's run folder: group-writable for the platform, where launch files for that slot go.
func RunDir(slot string) (string, bool) {
	cfg, on := loadConfig()
	if !on || !slotNameMatches(slot) {
		return "", false
	}
	return filepath.Join(cfg.SlotRunRoot, slot), true
}

// IsSlotLaunch reports whether hint (a launch's working or home folder) belongs to a slot.
func IsSlotLaunch(hint string) bool {
	_, ok := SlotOf(hint)
	return ok
}

// TempDir is where launch files for hint go: the slot's run folder for a slot launch, else os.TempDir().
func TempDir(hint string) string {
	if slot, ok := SlotOf(hint); ok {
		if dir, ok := RunDir(slot); ok {
			return dir
		}
	}
	return os.TempDir()
}

// Mode widens an owner-only mode to the owner's group for a slot launch (0600 -> 0660, 0700 ->
// 0770): the slot reaches these files through its group and nobody else can. A mode that already
// grants group or other access is left alone.
func Mode(hint string, mode os.FileMode) os.FileMode {
	if !IsSlotLaunch(hint) || mode&0o077 != 0 {
		return mode // not a slot launch, or already shared on purpose
	}
	return mode | (mode&0o700)>>3
}

// CreateTemp is os.CreateTemp for hint's launch files, readable by the slot when it is one.
func CreateTemp(hint, pattern string) (*os.File, error) {
	if err := CheckLaunch(hint); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(TempDir(hint), pattern)
	if err != nil {
		return nil, err
	}
	if IsSlotLaunch(hint) {
		if err := f.Chmod(0o660); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return nil, err
		}
	}
	return f, nil
}

// MkdirTemp is os.MkdirTemp for hint's launch files, enterable by the slot when it is one.
func MkdirTemp(hint, pattern string) (string, error) {
	if err := CheckLaunch(hint); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(TempDir(hint), pattern)
	if err != nil {
		return "", err
	}
	if IsSlotLaunch(hint) {
		if err := os.Chmod(dir, 0o770|os.ModeSetgid); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

type request struct {
	Argv   []string `json:"argv"`
	Cwd    string   `json:"cwd"`
	Env    []string `json:"env"`
	Userns bool     `json:"userns,omitempty"`
	FD3    string   `json:"fd3,omitempty"`
}

// ErrNotSlot is returned by WrapCmd for a launch that does not belong to a slot.
var ErrNotSlot = errors.New("not a slot launch")

// WrapCmd rewrites cmd, in place, to run as the slot of hint through sudo and slotctl. The request
// (program, folder, environment) is left in a file in the slot's run folder, so the command keeps
// its own standard input and output. env is the complete environment; when nil a minimal one is used
// (cmd.Env nil would otherwise mean "the platform's whole environment"). It returns the cleanup that
// removes the request file if the command never starts.
// lookupSlotUID returns a slot account's numeric uid; a variable so tests can run without real accounts.
var lookupSlotUID = func(name string) (string, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return "", err
	}
	return u.Uid, nil
}

// WithSlotDocker returns env with DOCKER_HOST set to the slot's own Docker socket when this host gives every slot
// one (config slot_docker); otherwise env is unchanged. The platform's own DOCKER_HOST (its account's socket, in a
// folder only that account can open) is replaced, never passed through.
func WithSlotDocker(env []string, slot string) []string {
	cfg, ok := loadConfigUnchecked()
	if !ok || !cfg.SlotDocker {
		return env
	}
	uid, err := lookupSlotUID(slot)
	if err != nil || uid == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "DOCKER_HOST=") {
			out = append(out, entry)
		}
	}
	return append(out, "DOCKER_HOST=unix:///run/user/"+uid+"/docker.sock")
}

func WrapCmd(cmd *exec.Cmd, hint string, env []string) (cleanup func(), err error) {
	slot, ok, blocked := SlotOfChecked(hint)
	if blocked != nil {
		return func() {}, blocked
	}
	if !ok {
		return func() {}, ErrNotSlot
	}
	runDir, _ := RunDir(slot)
	if cmd == nil || len(cmd.Args) == 0 {
		return func() {}, errors.New("empty command")
	}
	if env == nil {
		env = cmd.Env
	}
	if env == nil {
		env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	}
	env = WithSlotDocker(env, slot)
	cwd := cmd.Dir
	if cwd == "" {
		cwd = runDir
	}
	body, err := json.Marshal(request{Argv: append([]string(nil), cmd.Args...), Cwd: cwd, Env: env})
	if err != nil {
		return func() {}, err
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return func() {}, err
	}
	file := filepath.Join(runDir, "req-"+hex.EncodeToString(token)+".json")
	if err := os.WriteFile(file, body, 0o660); err != nil {
		return func() {}, fmt.Errorf("write the slot launch request: %w", err)
	}
	if err := os.Chmod(file, 0o660); err != nil {
		_ = os.Remove(file)
		return func() {}, err
	}
	cmd.Path = sudoPath
	cmd.Args = []string{sudoPath, "-n", "-u", slot, slotctl(), "exec", "--request-file", file}
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = "/"
	// sudo must pass TERM to slotctl, which can signal its own child group.
	// Killing the outer group outright leaves the slot's Muse process holding
	// its native session (PLAT-872-119). Match the host slot wrapper's stop path.
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { return terminateSlotCommand(cmd) }
		if cmd.WaitDelay == 0 {
			cmd.WaitDelay = 6 * time.Second
		}
	}
	return func() { _ = os.Remove(file) }, nil
}

var grantMu sync.Mutex

func setfacl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "setfacl", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("setfacl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// traverse lets slot walk to path through every folder above it that is not already world-searchable,
// by an access entry for that slot alone (no other slot gains anything).
func traverse(slot, path string) error {
	var dirs []string
	for dir := filepath.Dir(filepath.Clean(path)); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		dirs = append([]string{dir}, dirs...)
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o001 != 0 {
			continue
		}
		if groupOwns(slot, dir) {
			// The slot's group already has the folder (its own project or state folder). A named entry
			// for the slot would override that group access with search-only, so none is added, and
			// one left by an earlier launch is removed.
			_ = setfacl("-x", "u:"+slot, dir)
			continue
		}
		if err := setfacl("-m", "u:"+slot+":--x", dir); err != nil {
			return err
		}
	}
	return nil
}

// groupOwns reports whether dir belongs to the slot's own group with group search access.
func groupOwns(slot, dir string) bool {
	group, err := user.LookupGroup(slot)
	if err != nil {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o010 == 0 {
		return false
	}
	gid, ok := fileGID(info)
	return ok && strconv.Itoa(gid) == group.Gid
}

// groupCovers reports whether path belongs to the slot's own group and the group's permission bits already give
// what a grant asks for: read (and search, for a folder), plus write when asked.
func groupCovers(slot, path string, info os.FileInfo, write bool) bool {
	gid, ok := fileGID(info)
	if !ok {
		return false
	}
	// Any group the slot belongs to, not only its own: a workflow folder owned by the shared group (slotshared, rwx)
	// took a read-only entry for slot01, which outranked the group, and its steps could not write their run folder
	// ("Permission denied: …/runs/…/route_selection.json", 2 Nov).
	account, err := user.Lookup(slot)
	if err != nil {
		return false
	}
	groups, err := account.GroupIds()
	if err != nil {
		return false
	}
	member := false
	for _, id := range groups {
		member = member || id == strconv.Itoa(gid)
	}
	if !member {
		return false
	}
	need := os.FileMode(0o040)
	if write {
		need |= 0o020
	}
	if info.IsDir() {
		need |= 0o010
	}
	return info.Mode().Perm()&need == need
}

// GrantAccess gives one slot read and write on a shared file the platform owns (a CLI login that
// rotates its tokens and so cannot be copied), and lets it reach the file. Done once per slot and path.
func GrantAccess(slot, path string) error {
	return grant(slot, path, true)
}

// GrantRuntime gives one slot access to a file or folder the platform prepared for a launch (MCP
// config, hooks, status file): read, plus write when the CLI writes there. A missing path is skipped,
// never created: the adapter makes it first. Something already readable by everyone needs nothing but a way in.
func GrantRuntime(slot, path string, write bool) error {
	return grant(slot, path, write)
}

func grant(slot, path string, write bool) error {
	if !slotNameMatches(slot) {
		return fmt.Errorf("invalid slot %q", slot)
	}
	// Applied at every launch, never cached: an adapter that re-tightens its folder (chmod 0700) resets
	// the ACL mask and cancels the entry, so a remembered grant would be wrong.
	grantMu.Lock()
	defer grantMu.Unlock()
	return grantLocked(slot, path, write)
}

// sharedGrants remembers the shared folders (another person's Crew or Code) already granted to a slot in
// this run, and sharedGrantMu keeps those long recursive passes apart from the launch's own grants.
var (
	sharedGrants  sync.Map
	sharedGrantMu sync.Mutex
)

// GrantSharedOnce gives a slot access to a folder someone shared with its user, once per server run and
// in the background. A large Crew (hundreds of thousands of files) took ~20s of setfacl on every launch,
// before the CLI started, and never finished where a few files belong to another account. The launch never
// waits: the slot reaches what its group allows, and the default ACL covers files made later. report gets
// what could not be granted.
func GrantSharedOnce(slot, path string, write bool, report func(error)) {
	if !slotNameMatches(slot) {
		return
	}
	key := slot + "\x00" + filepath.Clean(path) + "\x00" + strconv.FormatBool(write)
	if _, done := sharedGrants.LoadOrStore(key, true); done {
		return
	}
	go func() {
		sharedGrantMu.Lock()
		defer sharedGrantMu.Unlock()
		if err := grantLocked(slot, path, write); err != nil && report != nil {
			report(err)
		}
	}()
}

func grantLocked(slot, path string, write bool) error {
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if os.IsNotExist(err) && write {
		// Never create it: a grant does not say whether it is a file or a folder, and a missing folder grant made
		// here as an empty file left a Crew's db/, assets/, reports/ … as 0-byte files that Files could not open
		// (5 Nov). Whoever owns the path makes it (as a folder or file) before granting.
		log.Printf("[SLOTFS] grant skipped: slot=%s path=%s does not exist yet", slot, clean)
		return nil
	}
	if err != nil {
		return nil // nothing there to grant (a path granted "in case")
	}
	if err := traverse(slot, clean); err != nil {
		return err
	}
	// Something already the slot's through its group (its own CLI home, made group slot by ShareTree) needs no
	// entry: a named-user entry outranks the group's, so a read grant there left the slot unable to write its own
	// temp folder (Cursor's slot launch: "agentworks-private-pts-*.json: permission denied", PLAT-872-86). Drop one
	// an earlier launch left.
	if groupCovers(slot, clean, info, write) {
		if info.IsDir() {
			_ = setfacl("-d", "-x", "u:"+slot, clean)
		}
		_ = setfacl("-x", "u:"+slot, clean)
		return nil
	}
	perm := "r"
	if write {
		perm = "rw"
	}
	if info.IsDir() {
		perm = strings.Replace(perm, "r", "rX", 1)
		if write {
			perm = "rwX"
		}
		if !write && info.Mode().Perm()&0o005 == 0o005 {
			return nil // a folder everyone can read and enter
		}
		// Both passes run even when a few files refuse (another account owns them): the default ACL is what
		// gives files made later the same access.
		return errors.Join(setfacl("-R", "-m", "u:"+slot+":"+perm, clean), setfacl("-R", "-d", "-m", "u:"+slot+":"+perm, clean))
	} else {
		if !write && info.Mode().Perm()&0o004 != 0 {
			// Everyone may read it. A read-only entry an earlier launch left (while the file was private) outranks
			// "other", so once a CLI update made the program runnable by everyone again, those slots still could
			// not run it (Codex: "Permission denied", exit 126). Drop such an entry from a program.
			if info.Mode().Perm()&0o001 != 0 {
				_ = setfacl("-x", "u:"+slot, clean)
			}
			return nil
		}
		// A program (the CLI's own launcher) must stay runnable for the slot, not just readable.
		if info.Mode().Perm()&0o111 != 0 {
			perm += "x"
		}
		if err := setfacl("-m", "u:"+slot+":"+perm, clean); err != nil {
			return err
		}
	}
	return nil
}

// On reports whether this host runs CLI launches as slots at all (its slot allow-list exists; the
// user decides whether a given launch is one).
func On() bool {
	_, ok := loadConfigUnchecked()
	return ok
}

// ShareTree gives a slot launch's group full use of a tree the platform just prepared (a CLI's
// config folder): the folder's files are made group-owned by the slot's group and group read-write
// (and group-searchable for folders), with the setgid bit on folders so new files stay in that group.
// Nothing for a launch that is not a slot's.
func ShareTree(hint, root string) error {
	slot, ok := SlotOf(hint)
	if !ok {
		return nil
	}
	group, err := user.LookupGroup(slot)
	if err != nil {
		return fmt.Errorf("group %s: %w", slot, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// What the slot made itself (a CLI cache, its sessions) is already the slot's, and only its owner or root may
		// change it: skip it, with everything under it (PLAT-872-90: the next launch failed "lchown ...: operation not
		// permitted").
		if uid, ok := fileUID(info); ok && uid != os.Getuid() {
			if info.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := os.Lchown(path, -1, gid); err != nil {
			return err
		}
		mode := info.Mode().Perm() | 0o060
		if info.IsDir() {
			return os.Chmod(path, mode|0o010|os.ModeSetgid)
		}
		if info.Mode().Perm()&0o100 != 0 {
			mode |= 0o010
		}
		return os.Chmod(path, mode)
	})
}

// LatestLaunchStderrLine is the first line of the newest <slot run folder>/last-launch.stderr
// written within maxAge, or "" (no slots, no such file, an empty one). The slot
// launcher records there why a command it was asked to start did not run.
func LatestLaunchStderrLine(maxAge time.Duration) string {
	cfg, on := loadConfig()
	if !on {
		return ""
	}
	return latestLaunchStderrLineIn(cfg.SlotRunRoot, maxAge, time.Now())
}

func latestLaunchStderrLineIn(root string, maxAge time.Duration, now time.Time) string {
	matches, _ := filepath.Glob(filepath.Join(root, "*", "last-launch.stderr"))
	var newest string
	var newestTime time.Time
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || now.Sub(info.ModTime()) > maxAge || !info.ModTime().After(newestTime) {
			continue
		}
		newest, newestTime = match, info.ModTime()
	}
	if newest == "" {
		return ""
	}
	data, err := os.ReadFile(newest) // #nosec G304 -- the slot launcher's own log
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	if len(line) > 300 {
		line = line[:300]
	}
	return strings.TrimSpace(line)
}
