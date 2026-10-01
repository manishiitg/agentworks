//go:build linux

// slotctl runs as a slot account (the platform reaches it through sudo) and starts one program on
// the platform's behalf, after checking the request against a root-owned allow-list.
//
//	sudo -n -u slotNN /usr/local/libexec/agentworks/slotctl exec   < request.json
//	sudo -n -u slotNN /usr/local/libexec/agentworks/slotctl exec --request-file <path in the slot run folder>
package main

import (
	"fmt"
	"os"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "exec" || (len(args) != 1 && !(len(args) == 3 && args[1] == "--request-file")) {
		fmt.Fprintln(os.Stderr, "usage: slotctl exec | slotctl exec --request-file <path>")
		os.Exit(2)
	}
	cfg, err := slots.LoadExecConfig(slots.ConfigBesideExecutable())
	if err != nil {
		fmt.Fprintf(os.Stderr, "slotctl: no usable allow-list: %v\n", err)
		os.Exit(125)
	}
	if len(args) == 3 {
		os.Exit(slots.RunExecFile(args[2], os.Stdin, os.Stdout, os.Stderr, cfg))
	}
	os.Exit(slots.RunExec(os.Stdin, os.Stdout, os.Stderr, cfg))
}
