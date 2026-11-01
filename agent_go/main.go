package main

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/nativersync"
	"os"
)

func main() {
	if nativersync.RunPipe(os.Args[1:]) {
		return
	}
	// Entry point logging handled by the command execution
	cmd.Execute()
}
