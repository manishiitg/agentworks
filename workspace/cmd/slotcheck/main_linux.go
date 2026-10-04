//go:build linux

// slotcheck is the read-only deploy self-test of the per-user slot chain (PLAT-478). It runs as the workspace
// service account, at the end of every deploy of a slot-enabled server and on its own (`./deploy.sh slotcheck
// <server>`), and exits 1 when any check fails. See workspace/slotcheck.
//
//	slotcheck --docs <docs root> --app <product folder> [--runner <launcher>] [--test-slot slotNN]
//
// The slotctl allow-list, launcher and slot table are the ones the service uses: AGENTWORKS_SLOTCTL_CONFIG,
// AGENTWORKS_SLOTCTL and AGENTWORKS_SLOTS_FILE (defaults as in the slots package).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/slotcheck"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

func main() {
	docs := flag.String("docs", "", "the workspace docs root the service uses")
	app := flag.String("app", "", "the product folder holding releases/")
	runner := flag.String("runner", "", "the Landlock launcher (default: video-studio-landlock-runner beside this program)")
	testSlot := flag.String("test-slot", "", "the dedicated test slot (default: the highest-numbered unassigned slot)")
	flag.Parse()
	if *docs == "" {
		fmt.Fprintln(os.Stderr, "usage: slotcheck --docs <docs root> --app <product folder> [--runner <launcher>] [--test-slot slotNN]")
		os.Exit(2)
	}
	if *runner == "" {
		if exe, err := os.Executable(); err == nil {
			*runner = filepath.Join(filepath.Dir(exe), "video-studio-landlock-runner")
		}
	}
	// The service starts the launcher by its real path (beside /proc/self/exe), which is what allowed_exec must match.
	if resolved, err := filepath.EvalSymlinks(*runner); err == nil {
		*runner = resolved
	}
	// The slot environment of this process is the service's (the deploy passes it); make the launcher choice explicit.
	_ = os.Setenv("AGENTWORKS_LANDLOCK_RUNNER", *runner)
	config := slots.ConfigPath()
	if cfg, err := slots.LoadExecConfig(config); err == nil {
		slots.SetPrefix(cfg.SlotPrefix)
	}
	table := strings.TrimSpace(os.Getenv(slots.EnvTableFile))
	if table == "" {
		table = slots.DefaultTableFile
	}
	gids := []int{os.Getgid()}
	if groups, err := os.Getgroups(); err == nil {
		gids = append(gids, groups...)
	}
	rows := slotcheck.Check(context.Background(), slotcheck.Options{
		DocsRoot:      *docs,
		AppDir:        *app,
		Runner:        *runner,
		SlotctlConfig: config,
		SlotTable:     table,
		TableOwnerUID: 0,
		ServiceGIDs:   gids,
		TestSlot:      *testSlot,
		Run:           slotcheck.RunThroughShellTool,
		RunCommand:    slotcheck.RunCommandThroughShellTool,
		Lookup:        slotcheck.LookupAccount,
	})
	fmt.Print(slotcheck.Format(rows))
	if slotcheck.Failed(rows) {
		os.Exit(1)
	}
}
