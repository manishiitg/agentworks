//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

func main() {
	configPath := flag.String("config", "", "path to a Landlock policy")
	flag.Parse()
	if *configPath == "" || flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "SANDBOX_UNAVAILABLE: usage: landlock-runner --config <path> -- <command> [args...]")
		os.Exit(125)
	}
	config, err := os.Open(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "SANDBOX_UNAVAILABLE: read Landlock policy")
		os.Exit(125)
	}
	var policy security.LandlockPolicy
	err = json.NewDecoder(config).Decode(&policy)
	_ = config.Close()
	if *configPath == "/proc/self/fd/3" {
		_ = unix.Close(3)
	}
	_ = os.Remove(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "SANDBOX_UNAVAILABLE: decode Landlock policy")
		os.Exit(125)
	}
	if err := security.RunLandlockLauncher(policy, flag.Args()); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}
