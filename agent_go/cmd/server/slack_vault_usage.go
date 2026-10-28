package server

import (
	"net/http"
	"sort"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Usage is saved routing, including paused bots. Naming a destination never
// grants access to it; filter with the same current access as Slack callers.
type slackVaultBotUsage struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

func (api *StreamingAPI) slackVaultBotUsages(r *http.Request, conn services.SlackConnection, isDefault bool, cache map[string]*slackVaultBotUsage) []slackVaultBotUsage {
	out := []slackVaultBotUsage{}
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		return out
	}
	var refs []services.SlackTargetRef
	if conn.WorkspacePath != "" {
		refs = slackOwnBotTargets(conn)
	} else if isDefault {
		// Reuse the routing resolver, including target opt-ins, allowed products
		// and channel routes. Non-default unscoped bots inherit none of these.
		for _, target := range api.slackDMTargets(r.Context(), conn.ID).Targets {
			refs = appendSlackRef(refs, target.Ref)
		}
	}
	for _, stored := range refs {
		ref := currentSlackRef(stored)
		key := ref.Key()
		usage, checked := cache[key]
		if !checked {
			cache[key] = nil
			if claims.AccessToken != nil {
				if _, err := personalConnectionItemScope(r, ref.WorkspacePath, false, "Slack", "Slack"); err != nil {
					continue
				}
			}
			if !slackTargetExists(r.Context(), ref) {
				continue
			}
			route, err := api.slackDestinationRoute(r.Context(), ref.WorkspacePath, ref.ProfileID)
			if err != nil || route == nil || !api.slackCanReach(r.Context(), claims.UserID, *route) {
				continue
			}
			usage = &slackVaultBotUsage{Label: slackDestinationLabel(r.Context(), ref.WorkspacePath, ref.ProfileID), Kind: services.SlackTargetProduct(ref)}
			cache[key] = usage
		}
		if usage != nil {
			out = append(out, *usage)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Label < out[j].Label
	})
	return out
}
