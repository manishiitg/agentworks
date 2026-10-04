package slotcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// Levels of the self-test. Basic is what every deploy runs; Full adds the checks below, which a person otherwise
// runs by hand in chats (PLAT-480): a live tmux server on the test slot that no slot command may reach, the Python
// helpers, and the refusals a workflow chat (the app account) must get. Everything runs on the dedicated TEST slot
// (an unassigned account), so no user's slot, shell or chat is touched.
const (
	LevelBasic = "basic"
	LevelFull  = "full"
)

// TmuxFunc runs `tmux -S <socket> <args...>` as a slot account through slotctl (what the platform's own tmux front-end
// does), returning the combined output.
type TmuxFunc func(ctx context.Context, slot, socket string, args ...string) (string, error)

// refusedCheck runs one command through the shell tool chain and requires it to be REFUSED (a non-zero exit code of
// the command itself). "started" first and the exit code last make a launcher that failed to start a FAIL.
func refusedCheck(ctx context.Context, run func(context.Context, Probe, string) (string, string, error), probe Probe, name, command, what string) Row {
	slot := probe.Slot
	if slot == "" {
		slot = "app"
	}
	stdout, stderr, err := run(ctx, probe, "echo started; "+command+"; echo rc=$?")
	out := strings.Fields(stdout)
	switch {
	case err != nil || len(out) < 2 || out[0] != "started":
		why := firstLine(stderr)
		if why == "" && err != nil {
			why = shortErr(err)
		}
		return Row{Fail, name, slot, "the command did not run, so nothing was proven: " + orNone(why), "fix the sandbox chain first (see the pwd checks above)"}
	case out[len(out)-1] == "rc=0":
		return Row{Fail, name, slot, "a sandboxed command could " + what, "PLAT-480: the sandbox grants too much; do not leave this release live"}
	default:
		return Row{Pass, name, slot, "refused: cannot " + what, ""}
	}
}

// allowedCheck runs one command and requires it to SUCCEED (a feature the sandbox must keep working).
func allowedCheck(ctx context.Context, run func(context.Context, Probe, string) (string, string, error), probe Probe, name, command, what string) Row {
	slot := probe.Slot
	if slot == "" {
		slot = "app"
	}
	stdout, stderr, err := run(ctx, probe, "echo started; "+command+"; echo rc=$?")
	out := strings.Fields(stdout)
	if err != nil || len(out) < 2 || out[0] != "started" || out[len(out)-1] != "rc=0" {
		why := firstLine(stderr)
		if why == "" && err != nil {
			why = shortErr(err)
		}
		return Row{Fail, name, slot, "cannot " + what + ": " + orNone(why), "PLAT-480: a sandbox change broke this; do not leave this release live"}
	}
	return Row{Pass, name, slot, what + ": works", ""}
}

// fullChecks are the extended (opt-in) checks. testSlot may be "" (no unassigned slot): the slot checks are skipped.
func fullChecks(ctx context.Context, opts Options, cfg slots.ExecConfig, docs, workflow, testSlot string) []Row {
	if opts.RunCommand == nil {
		return []Row{{Skip, "full-checks", "-", "no command runner: the extended checks need the real chain", ""}}
	}
	var rows []Row
	rows = append(rows, tmuxLiveCheck(ctx, opts, cfg, testSlot)...)
	rows = append(rows, helperChecks(ctx, opts, cfg, docs, testSlot)...)
	rows = append(rows, workflowChatChecks(ctx, opts, docs, workflow)...)
	return rows
}

// testSlotProbe is a probe for the test slot, run from its own state folder (it owns no project).
func testSlotProbe(cfg slots.ExecConfig, docs, slot string) (Probe, bool) {
	if slot == "" || cfg.SlotStateRoot == "" {
		return Probe{}, false
	}
	dir := cfg.SlotStateDir(slot)
	return Probe{Slot: slot, Kind: KindCode, Dir: dir, ReadPaths: []string{dir}, DocsRoot: docs, BrowserSession: browserSession("project", dir)}, true
}

// tmuxLiveCheck starts a real tmux server for the test slot and requires that a confined command of that slot cannot
// reach it (the hole PLAT-480 F1 closed), after proving the server is alive from outside the sandbox.
func tmuxLiveCheck(ctx context.Context, opts Options, cfg slots.ExecConfig, slot string) []Row {
	const name = "tmux-socket-unreachable"
	probe, ok := testSlotProbe(cfg, opts.DocsRoot, slot)
	if !ok || cfg.SlotRunRoot == "" {
		return []Row{{Skip, name, "-", "no test slot or no slot run root on this host", ""}}
	}
	if opts.TmuxControl == nil {
		return []Row{{Skip, name, slot, "no tmux control for this build", ""}}
	}
	socket := slots.SlotSocket(cfg.SlotRunRoot, slot)
	session := fmt.Sprintf("slotcheck-%d", time.Now().UnixNano())
	defer func() { _, _ = opts.TmuxControl(context.Background(), slot, socket, "kill-server") }()
	if out, err := opts.TmuxControl(ctx, slot, socket, "new-session", "-d", "-s", session, "sleep 120"); err != nil {
		return []Row{{Fail, name, slot, "could not start a test tmux server as the slot, so nothing was proven: " + firstLine(out) + " " + shortErr(err), "check slotctl's tmux allow-list and the slot's run folder (provision-slots.sh init)"}}
	}
	if out, err := opts.TmuxControl(ctx, slot, socket, "list-sessions"); err != nil || !strings.Contains(out, session) {
		return []Row{{Fail, name, slot, "the test tmux server is not alive from outside the sandbox, so nothing was proven: " + firstLine(out), "check the slot's tmux (slottmux, slotctl)"}}
	}
	// Refused = the confined `tmux list-sessions` fails (it cannot connect). rc=0 would mean it reached the server.
	return []Row{refusedCheck(ctx, opts.RunCommand, probe, name, "tmux -S "+shQuote(socket)+" list-sessions >/dev/null 2>&1", "reach the slot's live tmux server")}
}

// helperChecks proves the Python helpers every slot command can import are still reachable (PLAT-428, PLAT-480:
// hiding the run folder once hid them).
func helperChecks(ctx context.Context, opts Options, cfg slots.ExecConfig, docs, slot string) []Row {
	probe, ok := testSlotProbe(cfg, docs, slot)
	if !ok {
		return []Row{{Skip, "slot-python-helpers", "-", "no test slot", ""}}
	}
	find := `found=0; for d in $(echo "$PYTHONPATH" | tr ':' ' '); do if test -f "$d/agentworks_output.py" && test -f "$d/agentworks_db.py"; then found=1; fi; done; test $found = 1`
	return []Row{allowedCheck(ctx, opts.RunCommand, probe, "slot-python-helpers", find, "import the output and database helpers")}
}

// workflowChatChecks run the refusals a workflow (Goals) chat must get. Those chats run as the app account, not a
// slot, with the workflow folder as their project.
func workflowChatChecks(ctx context.Context, opts Options, docs, workflow string) []Row {
	if workflow == "" {
		return []Row{{Skip, "wf-checks", "app", "no workflow folder on this host to run from", ""}}
	}
	// No BrowserSession: for the app account the sandbox would create the workflow's browser profile folder, and the
	// self-test changes nothing on the host.
	probe := Probe{Slot: "", Kind: KindWorkflow, Dir: workflow, ReadPaths: []string{workflow}, DocsRoot: docs}
	stamp := fmt.Sprintf(".slotcheck-wf-%d", time.Now().UnixNano())
	outside := filepath.Join(filepath.Dir(workflow), stamp)
	var rows []Row
	rows = append(rows, refusedCheck(ctx, opts.RunCommand, probe, "wf-write-outside", "touch "+shQuote(outside)+" 2>/dev/null; rc=$?; rm -f "+shQuote(outside)+" 2>/dev/null; (exit $rc)", "create a file next to the workflow folder"))
	if other := siblingWorkflow(workflow); other != "" {
		rows = append(rows, refusedCheck(ctx, opts.RunCommand, probe, "wf-read-other-workflow", "cat "+shQuote(filepath.Join(other, "workflow.json"))+" >/dev/null 2>&1", "read another workflow's files"))
	}
	rows = append(rows, refusedCheck(ctx, opts.RunCommand, probe, "wf-list-users", "ls "+shQuote(filepath.Join(docs, "_users"))+" >/dev/null 2>&1", "list the users' folders"))
	if opts.AppDir != "" {
		rows = append(rows,
			refusedCheck(ctx, opts.RunCommand, probe, "wf-list-state", "ls "+shQuote(filepath.Join(opts.AppDir, "state"))+" >/dev/null 2>&1", "list the app state folder"),
			refusedCheck(ctx, opts.RunCommand, probe, "wf-read-env", "head -c 1 "+shQuote(filepath.Join(opts.AppDir, ".env"))+" >/dev/null 2>&1", "read the app's environment file"))
	}
	if shared := filepath.Join(docs, "_users", "_shared", "workflow_secrets"); dirExists(shared) {
		rows = append(rows, refusedCheck(ctx, opts.RunCommand, probe, "wf-list-shared-secrets", "ls "+shQuote(shared)+" >/dev/null 2>&1", "list the shared workflow secrets"))
	}
	// The app account's own tmux server runs coding CLIs; its default socket must not be in the command's view
	// (the private /tmp hides it, PLAT-364).
	rows = append(rows, refusedCheck(ctx, opts.RunCommand, probe, "wf-app-tmux-socket", `test -S "/tmp/tmux-$(id -u)/default"`, "see the app account's tmux socket"))
	return rows
}

func siblingWorkflow(workflow string) string {
	entries, err := os.ReadDir(filepath.Dir(workflow))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		dir := filepath.Join(filepath.Dir(workflow), e.Name())
		if e.IsDir() && dir != workflow {
			if _, err := os.Stat(filepath.Join(dir, "workflow.json")); err == nil {
				return dir
			}
		}
	}
	return ""
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
