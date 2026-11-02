// The private journal helper is installed beside slotctl. It is never setuid:
// sudo already started slotctl as the owner's assigned account.
package main

import (
	"fmt"
	"os"
	"os/user"

	"github.com/manishiitg/coding-agent-loop/workspace/nativesession"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

func main() {
	me, err := user.Current()
	if err != nil || me.Uid == "0" || me.Uid != fmt.Sprint(os.Getuid()) {
		fmt.Fprintln(os.Stderr, "native session helper requires an assigned account")
		os.Exit(125)
	}
	cfg, err := slots.LoadNativeExecConfig(slots.ConfigBesideExecutable())
	if err == nil && !slots.ValidSlot(me.Username) {
		err = fmt.Errorf("account is outside this installation")
	}
	if err == nil {
		// Provider filesystem helpers must use this installation's policy,
		// never an environment value supplied by an invoking slot account.
		err = os.Setenv(slots.EnvConfig, slots.ConfigBesideExecutable())
	}
	if err == nil {
		err = nativesession.Run(os.Stdin, os.Stdout, cfg, me.Username)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "native session:", err)
		os.Exit(125)
	}
}
