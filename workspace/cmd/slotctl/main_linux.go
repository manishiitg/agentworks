//go:build linux

// slotctl runs as a slot account (the platform reaches it through sudo) and starts one program on
// the platform's behalf, after checking the request against a root-owned allow-list.
//
//	sudo -n -u slotNN /usr/local/libexec/agentworks/slotctl exec   < request.json
package main

import (
	"fmt"
	"os"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "exec" {
		fmt.Fprintln(os.Stderr, "usage: slotctl exec (request on standard input)")
		os.Exit(2)
	}
	cfg, err := slots.LoadExecConfig(slots.DefaultSlotctlConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "slotctl: no usable allow-list: %v\n", err)
		os.Exit(125)
	}
	os.Exit(slots.RunExec(os.Stdin, os.Stdout, os.Stderr, cfg))
}
