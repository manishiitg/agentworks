// Package platformtools names the platform's public action tools. Member names
// are private registration identities, never alternate callable entry points.
package platformtools

import (
	_ "embed"
	"encoding/json"
	"strings"
)

type Action struct {
	Family      string   `json:"family"`
	Name        string   `json:"name"`
	Member      string   `json:"member"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
}

// DashboardActions is shared by native chat projection and external MCP.
//
//go:embed dashboard-actions.json
var dashboardSource []byte

var DashboardActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(dashboardSource, &actions); err != nil {
		panic(err)
	}
	return actions
}()

// Identity is used only while assembling trusted native definitions/policies.
// It never resolves a public call or accepts a retired tool name over MCP.
func Identity(member string) string {
	for _, action := range DashboardActions {
		if member == action.Member {
			return action.Family + "." + action.Name
		}
		for _, alias := range action.Aliases {
			if member == alias {
				return action.Family + "." + action.Name
			}
		}
	}
	return member
}

func PublicName(identity string) string {
	identity = Identity(identity)
	for _, action := range DashboardActions {
		if identity == action.Family+"."+action.Name {
			return action.Family
		}
	}
	return identity
}

func IsDashboardMember(name string) bool {
	id := Identity(name)
	return id != name && (strings.HasPrefix(id, "dashboard.") || strings.HasPrefix(id, "share."))
}
