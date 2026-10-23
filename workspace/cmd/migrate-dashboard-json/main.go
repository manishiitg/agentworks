// Run once while the workspace is stopped: migrate-dashboard-json WORKSPACE_DOCS_PATH.
package main

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/migrations/dashboardjson"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: migrate-dashboard-json WORKSPACE_DOCS_PATH")
		os.Exit(2)
	}
	count, backup, err := dashboardjson.Run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Migrated %d JSON dashboard revisions to TSX; backup: %s\n", count, backup)
}
