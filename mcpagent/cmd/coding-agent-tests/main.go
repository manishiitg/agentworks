// coding-agent-tests prints the real SDK certification test selector.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	agent "github.com/manishiitg/mcpagent/agent"
)

func main() {
	provider := flag.String("provider", "Codex", "provider evidence name, e.g. Codex")
	flag.Parse()
	names := map[string]bool{}
	for _, cert := range agent.Layer2P0Certifications {
		for _, p := range cert.Providers {
			if strings.EqualFold(p, *provider) {
				names[cert.TestName] = true
			}
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "no registered tests for provider")
		os.Exit(2)
	}
	sorted := []string{}
	for name := range names {
		sorted = append(sorted, regexp.QuoteMeta(name))
	}
	sort.Strings(sorted)
	fmt.Printf("^(%s)$/^(?i:%s)$\n", strings.Join(sorted, "|"), regexp.QuoteMeta(*provider))
}
