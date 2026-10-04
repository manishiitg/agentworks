// Package slotcheck is the deploy self-test of the per-user slot chain (PLAT-478): every deploy of a slot-enabled
// server proves that a shell command run as a slot account actually starts, through the same slotctl, sudo rule,
// Landlock launcher and grant builder the workspace shell tool uses.
//
// It is read-only. It reads the slotctl allow-list, the slot table (to count slots and pick folders; it prints slot
// names and counts only, never who holds a slot), folder modes and directory names, and it runs `pwd` as slot
// accounts in folders those accounts already own or are granted. The only files it causes are the short-lived
// per-command files the shell tool itself creates and removes for every command (the policy file, the scratch
// folder, the helper folder in the slot's run folder). It never changes a mode, an owner, a table or a config.
package slotcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// Status of one row.
const (
	Pass = "PASS"
	Fail = "FAIL"
	Skip = "SKIP"
	Warn = "WARN"
)

// Row is one line of the result table.
type Row struct {
	Status string
	Check  string
	Slot   string
	Detail string
	Fix    string
}

// Account is a slot account as the kernel sees it.
type Account struct {
	Name string
	UID  int
	GIDs []int
	Home string
}

// Probe is one slotted `pwd`: the folder and the grants the shell tool would send for it.
type Probe struct {
	Slot           string
	Kind           string
	Dir            string
	ReadPaths      []string
	WritePaths     []string
	BrowserSession string
	UserHome       string
	DocsRoot       string
}

// Options configure a check. Run, Lookup and the expected table owner are injectable for tests.
type Options struct {
	DocsRoot      string
	AppDir        string // the product folder that holds releases/ (for the fix hints)
	Runner        string // the Landlock launcher the shell tool starts (bin/video-studio-landlock-runner of this release)
	SlotctlConfig string
	SlotTable     string
	// TableOwnerUID is the owner the slot table must have (root, as provision-slots.sh writes it).
	TableOwnerUID int
	// ServiceGIDs are the groups of the account the workspace service runs as; the table's group must be one of them.
	ServiceGIDs []int
	// TraversalRoot is where the walk down to the launcher starts (default "/"; tests start at their temp root).
	TraversalRoot string
	// TestSlot overrides the dedicated test slot (default: the highest-numbered unassigned slot account).
	TestSlot string
	Run      func(context.Context, Probe) (stdout, stderr string, err error)
	// RunCommand runs one shell command for a probe the way Run does. When nil the confinement (deny) checks are skipped.
	RunCommand func(context.Context, Probe, string) (stdout, stderr string, err error)
	Lookup     func(name string) (Account, bool)
}

// Kinds of folder a slotted command is proven in.
const (
	KindDocs     = "docs-root"
	KindWorkflow = "workflow"
	KindCrew     = "crew-project"
	KindCode     = "code-project"
)

// Check runs every check and returns the table rows. It never stops at the first failure.
func Check(ctx context.Context, opts Options) []Row {
	var rows []Row
	add := func(r Row) { rows = append(rows, r) }

	docs := canonical(opts.DocsRoot)
	cfg, err := slots.LoadExecConfig(opts.SlotctlConfig)
	if err != nil {
		add(Row{Fail, "slotctl-config", "-", fmt.Sprintf("%s is missing or unreadable: %v", opts.SlotctlConfig, shortErr(err)), "run provision-slots.sh init on the host as root (RTS: deploy/aws-ec2/slots-admin.sh init)"})
		return rows
	}
	add(Row{Pass, "slotctl-config", "-", opts.SlotctlConfig + " readable", ""})
	if canonical(cfg.DocsRoot) != docs {
		add(Row{Fail, "slotctl-docs-root", "-", fmt.Sprintf("docs_root is %q, the service's docs root is %q", cfg.DocsRoot, docs), "re-run provision-slots.sh init with DOCS=" + docs})
	} else {
		add(Row{Pass, "slotctl-docs-root", "-", "docs_root = " + docs, ""})
	}
	if !cfg.AllowsCwd(docs) {
		add(Row{Fail, "slotctl-allowed-cwd", "-", "allowed_cwd does not cover the docs root " + docs, "PLAT-476: re-run provision-slots.sh init (allowed_cwd lists $DOCS), or add " + docs + " to allowed_cwd in " + opts.SlotctlConfig + " as root"})
	} else {
		add(Row{Pass, "slotctl-allowed-cwd", "-", "allowed_cwd covers the docs root", ""})
	}
	runner := opts.Runner
	if resolved, rerr := filepath.EvalSymlinks(runner); rerr == nil {
		runner = resolved
	}
	if !cfg.AllowsProgram(runner) {
		add(Row{Fail, "slotctl-allowed-exec", "-", "allowed_exec does not list the Landlock launcher " + runner, "re-run provision-slots.sh init (allowed_exec: " + filepath.Join(opts.AppDir, "releases", "*", "bin", filepath.Base(runner)) + ")"})
	} else {
		add(Row{Pass, "slotctl-allowed-exec", "-", "allowed_exec lists " + runner, ""})
	}

	table, tableRows := checkTable(opts)
	rows = append(rows, tableRows...)
	if table == nil {
		return rows
	}

	assigned := make([]string, 0, len(table.Slots))
	for slot := range table.Slots {
		assigned = append(assigned, slot)
	}
	sort.Strings(assigned)
	testSlot := opts.TestSlot
	if testSlot == "" {
		testSlot = pickTestSlot(table, opts.Lookup)
	}
	add(Row{Pass, "slot-table-count", "-", fmt.Sprintf("%d assigned slot(s); test slot %s", len(assigned), orNone(testSlot)), ""})

	probeSlots := append([]string{}, assigned...)
	if testSlot != "" && table.Slots[testSlot] == "" {
		probeSlots = append(probeSlots, testSlot)
	}
	if len(probeSlots) == 0 {
		add(Row{Fail, "slot-accounts", "-", "no assigned slot and no unassigned slot account to test with", "run provision-slots.sh init (it creates SLOT_COUNT accounts)"})
		return rows
	}

	workflow := firstChildDir(filepath.Join(docs, "Workflow"))
	for _, slot := range probeSlots {
		account, ok := opts.Lookup(slot)
		if !ok {
			add(Row{Fail, "slot-account", slot, "no such Linux account", "run provision-slots.sh init"})
			continue
		}
		if blocked, kind := firstBlocked(opts.TraversalRoot, runner, account); blocked != "" {
			fix := "chmod o+x " + blocked
			if filepath.Base(blocked) == "releases" {
				fix = "chmod 0711 " + blocked + " (the deploy does this on slot hosts: slots_release_traversal)"
			}
			if kind == "file" {
				fix = "chmod 0755 " + blocked
			}
			add(Row{Fail, "runner-reachable", slot, blocked + " is not " + map[string]string{"dir": "traversable", "file": "executable"}[kind] + " for " + slot, fix})
		} else {
			add(Row{Pass, "runner-reachable", slot, "every folder down to the launcher is traversable", ""})
		}
		user := table.Slots[slot]
		probes := probesFor(cfg, docs, workflow, slot, user, account)
		for _, probe := range probes {
			rows = append(rows, runProbe(ctx, opts, cfg, probe))
		}
		rows = append(rows, denyChecks(ctx, opts, docs, probes)...)
	}
	if releases := filepath.Join(opts.AppDir, "releases"); opts.AppDir != "" {
		if info, err := os.Stat(releases); err == nil && info.Mode().Perm()&0o004 != 0 {
			add(Row{Warn, "releases-listable", "-", releases + " is world-listable (" + fmt.Sprintf("%04o", info.Mode().Perm()) + ")", "chmod 0711 " + releases})
		}
	}
	return rows
}

func checkTable(opts Options) (*slots.Table, []Row) {
	var rows []Row
	info, err := os.Stat(opts.SlotTable)
	if err != nil {
		return nil, append(rows, Row{Fail, "slot-table", "-", "cannot stat " + opts.SlotTable + ": " + shortErr(err), "run provision-slots.sh init"})
	}
	st, _ := info.Sys().(*syscall.Stat_t)
	ok := true
	if st != nil && int(st.Uid) != opts.TableOwnerUID {
		ok = false
		rows = append(rows, Row{Fail, "slot-table-owner", "-", fmt.Sprintf("%s is owned by uid %d, not %d", opts.SlotTable, st.Uid, opts.TableOwnerUID), "chown root " + opts.SlotTable + " (as root; provision-slots.sh assign/release keep it)"})
	}
	if st != nil && !containsInt(opts.ServiceGIDs, int(st.Gid)) {
		ok = false
		rows = append(rows, Row{Fail, "slot-table-group", "-", fmt.Sprintf("%s has group gid %d, which the service account is not in (RTS 2026-10-04: root:root refused every slot user)", opts.SlotTable, st.Gid), "chgrp <service group> " + opts.SlotTable + " (as root)"})
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		ok = false
		rows = append(rows, Row{Fail, "slot-table-mode", "-", fmt.Sprintf("%s has mode %04o, not 0640", opts.SlotTable, perm), "chmod 0640 " + opts.SlotTable + " (as root)"})
	}
	table, err := slots.LoadTable(opts.SlotTable)
	if err != nil {
		return nil, append(rows, Row{Fail, "slot-table-read", "-", "the service account cannot read the slot table: " + shortErr(err), "chown root:<service group> " + opts.SlotTable + " && chmod 0640 " + opts.SlotTable + " (as root)"})
	}
	if ok {
		rows = append(rows, Row{Pass, "slot-table", "-", opts.SlotTable + " root-owned, service group, 0640, readable", ""})
	}
	return table, rows
}

// pickTestSlot is the highest-numbered slot account that exists and holds no user (provision-slots.sh creates
// SLOT_COUNT accounts, default 50; they are contiguous). It holds no user's data.
func pickTestSlot(table *slots.Table, lookup func(string) (Account, bool)) string {
	best := ""
	for n := 1; n <= 999; n++ {
		name := fmt.Sprintf("%s%02d", slots.Prefix(), n)
		if _, held := table.Slots[name]; held {
			continue
		}
		if _, ok := lookup(name); ok {
			best = name
		}
	}
	return best
}

func probesFor(cfg slots.ExecConfig, docs, workflow, slot, user string, account Account) []Probe {
	probes := []Probe{{Slot: slot, Kind: KindDocs, Dir: docs, DocsRoot: docs}}
	// Probes grant their folder READ-only: a write grant on the working folder makes the shell tool set up its tool
	// caches and home there (<folder>/.sandbox-cache), which would change the user's folder. Everything that decides
	// whether a command starts is the same: slotctl's folder check, the launcher, and the grant builder with the
	// folder's browser session (the PLAT-478 failure came from the browser grant, not from the folder's own).
	wf := Probe{Slot: slot, Kind: KindWorkflow, Dir: workflow, DocsRoot: docs}
	if workflow != "" {
		wf.ReadPaths = []string{workflow}
		wf.BrowserSession = browserSession("workflow", workflow)
	}
	probes = append(probes, wf)
	crewDir, codeDir := "", ""
	if user != "" {
		crewDir = firstChildDir(filepath.Join(docs, workspaceref.PhysicalPath(user, workspaceref.CrewProjectsRoot)))
		codeDir = firstChildDir(filepath.Join(docs, workspaceref.PhysicalPath(user, workspaceref.CodeProjectsRoot)))
	} else if cfg.SlotStateRoot != "" {
		// The test slot owns no project: prove the Crew and Code grant shapes in its own state folder.
		crewDir = cfg.SlotStateDir(slot)
		codeDir = cfg.SlotStateDir(slot)
	}
	crew := Probe{Slot: slot, Kind: KindCrew, Dir: crewDir, DocsRoot: docs}
	code := Probe{Slot: slot, Kind: KindCode, Dir: codeDir, DocsRoot: docs, UserHome: account.Home}
	for _, p := range []*Probe{&crew, &code} {
		if p.Dir != "" {
			p.ReadPaths = []string{p.Dir}
			p.BrowserSession = browserSession("project", p.Dir)
		}
	}
	return append(probes, crew, code)
}

// browserSession names a managed browser of this kind the way agent_go does (common.SandboxBrowserSession), so the
// probe carries the same browser grant request a real chat does.
func browserSession(kind, dir string) string {
	sum := sha256.Sum256([]byte(dir))
	session := kind + "-" + hex.EncodeToString(sum[:8]) + "--browser"
	if prefix := strings.TrimSpace(os.Getenv("AGENTWORKS_BROWSER_SESSION_PREFIX")); prefix != "" {
		session = prefix + "--" + session
	}
	return session
}

func runProbe(ctx context.Context, opts Options, cfg slots.ExecConfig, probe Probe) Row {
	check := "pwd-" + probe.Kind
	shown := display(probe.DocsRoot, probe.Dir)
	if probe.Dir == "" {
		return Row{Skip, check, probe.Slot, "no " + probe.Kind + " folder for this slot", ""}
	}
	if !cfg.AllowsCwd(probe.Dir) {
		return Row{Fail, check, probe.Slot, shown + " is outside allowed_cwd", "PLAT-476: re-run provision-slots.sh init"}
	}
	stdout, stderr, err := opts.Run(ctx, probe)
	got := strings.TrimSpace(stdout)
	if err == nil && (got == probe.Dir || canonical(got) == canonical(probe.Dir)) {
		return Row{Pass, check, probe.Slot, shown, ""}
	}
	why := firstLine(stderr)
	if why == "" && err != nil {
		why = shortErr(err)
	}
	if why == "" {
		why = fmt.Sprintf("pwd printed %q", got)
	}
	return Row{Fail, check, probe.Slot, shown + ": " + why, fixFor(why)}
}

func fixFor(why string) string {
	switch {
	case strings.Contains(why, "outside the allowed folders"):
		return "PLAT-476: allowed_cwd must list the docs root (provision-slots.sh init)"
	case strings.Contains(why, "inspect Landlock path"):
		return "PLAT-478: deploy a release whose launcher skips grants the slot cannot stat and whose grant builder drops app-private paths"
	case strings.Contains(why, "fork/exec") && strings.Contains(why, "permission denied"):
		return "PLAT-478: make <app>/releases 0711 (slots_release_traversal) so the slot can reach the launcher"
	case strings.Contains(why, "is not an allowed program"):
		return "re-run provision-slots.sh init (allowed_exec)"
	case strings.Contains(why, "sudo"):
		return "check the sudo rule /etc/sudoers.d/agentworks-slots* (provision-slots.sh init)"
	}
	return "read the slot chain in docs/bugs/pulse_platform/security-sandbox/plat-478.md"
}

// firstBlocked walks from / down to the launcher and returns the first folder the account cannot traverse (or the
// launcher itself when it cannot execute it), by the kernel's owner/group/other rule.
func firstBlocked(root, runner string, account Account) (string, string) {
	runner = filepath.Clean(runner)
	if root == "" {
		root = "/"
	}
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, runner)
	if err != nil || strings.HasPrefix(rel, "..") {
		return runner, "file"
	}
	if info, err := os.Stat(root); err == nil && !allowed(info, account) {
		return root, "dir"
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Stat(current)
		if err != nil {
			if i == len(parts)-1 {
				return current, "file"
			}
			return current, "dir"
		}
		if i == len(parts)-1 {
			if !allowed(info, account) {
				return current, "file"
			}
			continue
		}
		if !allowed(info, account) {
			return current, "dir"
		}
	}
	return "", ""
}

// allowed: the execute/search bit that applies to this account (owner, else group, else other). ACLs are not
// used on the slot hosts (provision-slots.sh sets plain modes and groups).
func allowed(info fs.FileInfo, account Account) bool {
	mode := info.Mode().Perm()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return mode&0o001 != 0
	}
	switch {
	case int(st.Uid) == account.UID:
		return mode&0o100 != 0
	case containsInt(account.GIDs, int(st.Gid)):
		return mode&0o010 != 0
	default:
		return mode&0o001 != 0
	}
}

// firstChildDir is the first real (not hidden, not a symlink) folder in dir by name, or "".
func firstChildDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		return canonical(filepath.Join(dir, entry.Name()))
	}
	return ""
}

// display shows a folder relative to the docs root with the user id left out (it is slot table content): a user's
// folder is shown as <user>/<path in their tree>.
func display(docs, dir string) string {
	rel, err := filepath.Rel(docs, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	if rel == "." {
		return "<docs>"
	}
	if ref, ok := workspaceref.Parse(filepath.ToSlash(rel)); ok && ref.HasOwner() {
		return "<docs>/<user>/" + ref.Logical()
	}
	return "<docs>/" + filepath.ToSlash(rel)
}

func canonical(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func shortErr(err error) string { return firstLine(err.Error()) }

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "SANDBOX_GRANT_SKIPPED:") {
			return line
		}
	}
	return ""
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Failed reports whether any row failed.
func Failed(rows []Row) bool {
	for _, r := range rows {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// Format renders the table and, after it, one "FAIL <what> <how to fix>" line per failure.
func Format(rows []Row) string {
	var b strings.Builder
	width := 6
	for _, r := range rows {
		if len(r.Check) > width {
			width = len(r.Check)
		}
	}
	fmt.Fprintf(&b, "%-4s  %-*s  %-7s  %s\n", "", width, "CHECK", "SLOT", "DETAIL")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-4s  %-*s  %-7s  %s\n", r.Status, width, r.Check, r.Slot, r.Detail)
	}
	pass, fail, skip := 0, 0, 0
	for _, r := range rows {
		switch r.Status {
		case Pass:
			pass++
		case Fail:
			fail++
		case Skip:
			skip++
		}
	}
	for _, r := range rows {
		if r.Status == Fail {
			slot := ""
			if r.Slot != "-" && r.Slot != "" {
				slot = " [" + r.Slot + "]"
			}
			fmt.Fprintf(&b, "FAIL %s%s: %s -- fix: %s\n", r.Check, slot, r.Detail, r.Fix)
		}
	}
	fmt.Fprintf(&b, "slot self-test: %d passed, %d failed, %d skipped\n", pass, fail, skip)
	return b.String()
}

// denyChecks proves a slotted command stays inside the folders it was granted. It runs a few commands through the same
// chain as the pwd probes, with only the probe's own folder granted, and requires each to be REFUSED. Every command
// prints "started" first and its exit code last, so a launcher that failed to start (which also "refuses") is a
// FAIL, never a pass. Nothing is changed: the one write attempt is removed if it ever succeeds (and fails the check).
func denyChecks(ctx context.Context, opts Options, docs string, probes []Probe) []Row {
	if opts.RunCommand == nil {
		return nil
	}
	var probe Probe
	for _, p := range probes {
		if (p.Kind == KindCrew || p.Kind == KindCode) && p.Dir != "" {
			probe = p
			break
		}
	}
	if probe.Dir == "" {
		return []Row{{Skip, "deny-checks", probes[0].Slot, "no project folder to run the confinement checks from", ""}}
	}
	type denial struct{ name, command, what string }
	stamp := fmt.Sprintf(".slotcheck-deny-%d", time.Now().UnixNano())
	outside := filepath.Join(docs, stamp)
	tests := []denial{
		{"deny-write-outside", "touch " + shQuote(outside) + " 2>/dev/null; rc=$?; rm -f " + shQuote(outside) + " 2>/dev/null; echo rc=$rc", "create a file in the docs root"},
		{"deny-list-users", "ls " + shQuote(filepath.Join(docs, "_users")) + " >/dev/null 2>&1; echo rc=$?", "list the other users' folders"},
	}
	if opts.AppDir != "" {
		tests = append(tests,
			denial{"deny-list-releases", "ls " + shQuote(filepath.Join(opts.AppDir, "releases")) + " >/dev/null 2>&1; echo rc=$?", "list the release folders"},
			denial{"deny-list-state", "ls " + shQuote(filepath.Join(opts.AppDir, "state")) + " >/dev/null 2>&1; echo rc=$?", "list the app state folder"})
	}
	var rows []Row
	for _, t := range tests {
		stdout, stderr, err := opts.RunCommand(ctx, probe, "echo started; "+t.command)
		out := strings.Fields(stdout)
		switch {
		case err != nil || len(out) < 2 || out[0] != "started":
			why := firstLine(stderr)
			if why == "" && err != nil {
				why = shortErr(err)
			}
			rows = append(rows, Row{Fail, t.name, probe.Slot, "the command did not run, so nothing was proven: " + orNone(why), "fix the slot chain first (see the pwd checks above)"})
		case out[len(out)-1] == "rc=0":
			rows = append(rows, Row{Fail, t.name, probe.Slot, "a slotted command could " + t.what, "PLAT-480: the sandbox grants too much; do not leave this release live"})
		default:
			rows = append(rows, Row{Pass, t.name, probe.Slot, "refused: cannot " + t.what, ""})
		}
	}
	return rows
}

// shQuote single-quotes a path for the shell.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
