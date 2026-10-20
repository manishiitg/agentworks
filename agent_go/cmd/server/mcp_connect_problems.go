package server

import (
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// mcpConnectProblem is why one MCP connection last failed to connect, in the service's own words when it gave any
// (Slack: "App is not enabled for Slack MCP server access"). A later successful connect clears it. Shown on the
// account row so a person can fix it without reading server logs (owner, 2026-10-20).
type mcpConnectProblem struct {
	Reason string
	At     time.Time
}

var mcpConnectProblems sync.Map // agent server name -> mcpConnectProblem

func init() {
	mcpclient.OnConnectResult = recordMCPConnectResult
}

func recordMCPConnectResult(serverName string, err error) {
	if err == nil {
		mcpConnectProblems.Delete(serverName)
		return
	}
	mcpConnectProblems.Store(serverName, mcpConnectProblem{Reason: plainMCPConnectReason(err.Error()), At: time.Now().UTC()})
}

// plainMCPConnectReason keeps what the service said and drops the transport wording around it.
func plainMCPConnectReason(message string) string {
	if _, said, ok := strings.Cut(message, "the server said: "); ok {
		for _, tail := range []string{": transport error", ": server returned 4xx", ": unauthorized (401)"} {
			if i := strings.Index(said, tail); i >= 0 {
				said = said[:i]
			}
		}
		message = said
	}
	message = strings.TrimSpace(message)
	if len(message) > 300 {
		message = message[:300] + "…"
	}
	return message
}

// mcpConnectProblemFor finds the latest problem of a person's saved connection by its id. The agent server name
// ends with "__<id>" (the owner part before it varies with the session).
func mcpConnectProblemFor(connectionID string) string {
	suffix := "__" + strings.TrimSpace(connectionID)
	var found mcpConnectProblem
	mcpConnectProblems.Range(func(key, value any) bool {
		if name, _ := key.(string); strings.HasSuffix(name, suffix) {
			if problem := value.(mcpConnectProblem); problem.At.After(found.At) {
				found = problem
			}
		}
		return true
	})
	return found.Reason
}
