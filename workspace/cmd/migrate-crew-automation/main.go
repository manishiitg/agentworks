// One-off held-refactor conversion; never run automatically at startup.
package main

import (
	"flag"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"log"
	"os"
)

func main() {
	docs := flag.String("docs", "", "selected instance's absolute workspace-docs directory")
	state := flag.String("state", "", "selected instance's absolute private state directory")
	flag.Parse()
	if *docs == "" || *state == "" {
		log.Fatal("supply --docs and --state for the stopped instance")
	}
	os.Setenv("AGENTWORKS_STATE_ROOT", *state)
	n, backup, err := crewroot.MigrateExecutableSources(*docs)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Moved %d authored paths; backup=%s\n", n, backup)
}
