package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// Consolidated cost view (Providers → Costs): recorded usage across workflows,
// Crews, and product projects the caller can see, split by scope and model.
// Ledger workflow IDs are folded into one row per work root. Totals are
// recomputed from visible rows; chat and unattributed activity is admin-only.

const (
	costOverviewKindWorkflow = "workflow"
	costOverviewKindCrew     = "crew"
	costOverviewKindProduct  = "product"
	costOverviewKindOther    = "other"

	costOverviewOtherID = "other"
	crewProjectsSegment = "Chats/Work/projects/"
)

type costOverviewItem struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	OwnerID string `json:"owner_id,omitempty"`
	costledger.WorkflowAggregate
	ByUser []*costOverviewActor `json:"by_user,omitempty"`
	ByBot  []*costOverviewBot   `json:"by_bot,omitempty"`
	ByMCP  []*costOverviewMCP   `json:"by_mcp,omitempty"`
}

type costOverviewUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	costledger.UserAggregate
	ByWork []*costOverviewWork `json:"by_work,omitempty"`
}

type costOverviewActor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	costledger.UserAggregate
}

type costOverviewWork struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	costledger.UserAggregate
}

type costOverviewBot struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Workflow string `json:"workflow"`
	costledger.BotAggregate
}

type costOverviewMCP struct {
	Server string `json:"server"`
	costledger.MCPAggregate
}

type costOverviewResponse struct {
	From       string                           `json:"from,omitempty"`
	To         string                           `json:"to,omitempty"`
	Total      costledger.Aggregate             `json:"total"`
	ByProvider map[string]*costledger.Aggregate `json:"by_provider"`
	ByModel    map[string]*costledger.Aggregate `json:"by_model"`
	Items      []*costOverviewItem              `json:"items"`
	ByUser     []*costOverviewUser              `json:"by_user"`
	ByBot      []*costOverviewBot               `json:"by_bot"`
	ByMCP      []*costOverviewMCP               `json:"by_mcp"`
	// IncludesOther reports whether chat/unattributed spend is in the view
	// (admins only), so the UI can say what the total covers.
	IncludesOther bool `json:"includes_other"`
}

// costOverviewRoot folds a ledger workflow_id into the row it belongs to.
func costOverviewRoot(workflowID string) (id, kind, name, ownerID string) {
	path := strings.Trim(strings.ReplaceAll(strings.TrimSpace(workflowID), "\\", "/"), "/")
	if rest, ok := strings.CutPrefix(path, "Workflow/"); ok {
		folder, _, _ := strings.Cut(rest, "/")
		if folder != "" {
			return "Workflow/" + folder, costOverviewKindWorkflow, folder, ""
		}
	}
	if i := strings.Index(path, crewProjectsSegment); i >= 0 && (i == 0 || path[i-1] == '/') {
		project, _, _ := strings.Cut(path[i+len(crewProjectsSegment):], "/")
		if project != "" {
			root := path[:i+len(crewProjectsSegment)] + project
			owner, _ := crewProjectOwnerID(root)
			return root, costOverviewKindCrew, project, owner
		}
	}
	// Product project usage can be recorded against its physical workspace,
	// e.g. _users/<owner>/Chats/Video Studio/projects/<project>. Do not fold
	// these into the admin-only "other" bucket: their owner should see them.
	parts := strings.Split(path, "/")
	if len(parts) >= 6 && parts[0] == "_users" && parts[1] != "" &&
		parts[2] == "Chats" && parts[3] != "" && parts[4] == "projects" && parts[5] != "" {
		return strings.Join(parts[:6], "/"), costOverviewKindProduct, parts[3] + " · " + parts[5], parts[1]
	}
	return costOverviewOtherID, costOverviewKindOther, "Unattributed activity", ""
}

func costOverviewProductVisible(id, userID string, admin bool) bool {
	_, kind, _, ownerID := costOverviewRoot(id)
	return kind == costOverviewKindProduct && (admin || (userID != "" && ownerID == sanitizeUserIDForPath(userID)))
}

func mergeWorkflowAggregate(target *costledger.WorkflowAggregate, source *costledger.WorkflowAggregate) {
	target.Aggregate.Merge(source.Aggregate)
	if target.ByScope == nil {
		target.ByScope = make(map[string]*costledger.Aggregate)
	}
	if target.ByModel == nil {
		target.ByModel = make(map[string]*costledger.Aggregate)
	}
	mergeAggregateMap(target.ByScope, source.ByScope)
	mergeAggregateMap(target.ByModel, source.ByModel)
}

func mergeAggregateMap(target, source map[string]*costledger.Aggregate) {
	for key, aggregate := range source {
		if aggregate == nil {
			continue
		}
		bucket, ok := target[key]
		if !ok {
			bucket = &costledger.Aggregate{}
			target[key] = bucket
		}
		bucket.Merge(*aggregate)
	}
}

func mergeUserAggregate(target *costledger.UserAggregate, source *costledger.UserAggregate) {
	target.Aggregate.Merge(source.Aggregate)
	if target.ByScope == nil {
		target.ByScope = make(map[string]*costledger.Aggregate)
	}
	if target.ByModel == nil {
		target.ByModel = make(map[string]*costledger.Aggregate)
	}
	mergeAggregateMap(target.ByScope, source.ByScope)
	mergeAggregateMap(target.ByModel, source.ByModel)
}

func costOverviewUserName(userID string) string {
	if userID == "" {
		return "Unattributed"
	}
	if strings.HasPrefix(userID, "bot-") {
		return "Bot · " + userID
	}
	return logUsernameForUserID(userID)
}

// buildCostOverview folds and filters a ledger summary. visible decides
// whether a workflow or Crew row is shown; includeOther gates chat and
// unattributed spend.
func buildCostOverview(summary *costledger.Summary, visible func(id, kind string) bool, includeOther bool) *costOverviewResponse {
	resp := &costOverviewResponse{
		ByProvider:    make(map[string]*costledger.Aggregate),
		ByModel:       make(map[string]*costledger.Aggregate),
		Items:         []*costOverviewItem{},
		ByUser:        []*costOverviewUser{},
		ByBot:         []*costOverviewBot{},
		ByMCP:         []*costOverviewMCP{},
		IncludesOther: includeOther,
	}
	if summary == nil {
		return resp
	}
	resp.From, resp.To = summary.From, summary.To
	items := make(map[string]*costOverviewItem)
	users := make(map[string]*costOverviewUser)
	itemUsers := make(map[string]map[string]*costOverviewActor)
	userWorks := make(map[string]map[string]*costOverviewWork)
	bots := make(map[string]*costOverviewBot)
	servers := make(map[string]*costOverviewMCP)
	itemServers := make(map[string]map[string]*costOverviewMCP)
	allowed := make(map[string]bool)
	for workflowID, aggregate := range summary.ByWorkflow {
		if aggregate == nil {
			continue
		}
		id, kind, name, ownerID := costOverviewRoot(workflowID)
		ok, seen := allowed[id]
		if !seen {
			ok = includeOther
			if kind != costOverviewKindOther {
				ok = visible(id, kind)
			}
			allowed[id] = ok
		}
		if !ok {
			continue
		}
		item, exists := items[id]
		if !exists {
			item = &costOverviewItem{ID: id, Kind: kind, Name: name, OwnerID: ownerID}
			items[id] = item
		}
		mergeWorkflowAggregate(&item.WorkflowAggregate, aggregate)
	}
	for workflowID, byUser := range summary.ByWorkflowUser {
		id, kind, name, _ := costOverviewRoot(workflowID)
		if !allowed[id] {
			continue
		}
		for userID, aggregate := range byUser {
			if aggregate == nil {
				continue
			}
			user := users[userID]
			if user == nil {
				user = &costOverviewUser{ID: userID, Name: costOverviewUserName(userID)}
				users[userID] = user
			}
			mergeUserAggregate(&user.UserAggregate, aggregate)
			if itemUsers[id] == nil {
				itemUsers[id] = make(map[string]*costOverviewActor)
			}
			actor := itemUsers[id][userID]
			if actor == nil {
				actor = &costOverviewActor{ID: userID, Name: user.Name}
				itemUsers[id][userID] = actor
			}
			mergeUserAggregate(&actor.UserAggregate, aggregate)
			if userWorks[userID] == nil {
				userWorks[userID] = make(map[string]*costOverviewWork)
			}
			work := userWorks[userID][id]
			if work == nil {
				work = &costOverviewWork{ID: id, Kind: kind, Name: name}
				userWorks[userID][id] = work
			}
			mergeUserAggregate(&work.UserAggregate, aggregate)
		}
	}
	for workflowID, byBot := range summary.ByWorkflowBot {
		root, _, workflowName, _ := costOverviewRoot(workflowID)
		if !allowed[root] {
			continue
		}
		for _, aggregate := range byBot {
			if aggregate == nil {
				continue
			}
			id := aggregate.Platform + ":" + aggregate.UserID + ":" + root
			bot := bots[id]
			if bot == nil {
				name := strings.ToUpper(aggregate.Platform[:1]) + aggregate.Platform[1:]
				if strings.HasPrefix(aggregate.UserID, "bot-") {
					routeID := strings.TrimPrefix(aggregate.UserID, "bot-"+aggregate.Platform+"-")
					if len(routeID) > 6 {
						routeID = routeID[len(routeID)-6:]
					}
					name += " · " + workflowName + " · " + routeID
				} else if aggregate.UserID != "" {
					name += " · " + logUsernameForUserID(aggregate.UserID) + " · " + workflowName
				} else {
					name += " · " + workflowName
				}
				bot = &costOverviewBot{ID: id, Name: name, Workflow: root, BotAggregate: costledger.BotAggregate{Platform: aggregate.Platform, UserID: aggregate.UserID}}
				bots[id] = bot
			}
			bot.Aggregate.Merge(aggregate.Aggregate)
		}
	}
	for workflowID, byServer := range summary.ByWorkflowMCP {
		root, _, _, _ := costOverviewRoot(workflowID)
		if !allowed[root] {
			continue
		}
		for server, aggregate := range byServer {
			if aggregate == nil {
				continue
			}
			bucket := servers[server]
			if bucket == nil {
				bucket = &costOverviewMCP{Server: server}
				servers[server] = bucket
			}
			bucket.Calls += aggregate.Calls
			bucket.UnpricedCalls += aggregate.UnpricedCalls
			bucket.RecordedCostUSD += aggregate.RecordedCostUSD
			if itemServers[root] == nil {
				itemServers[root] = make(map[string]*costOverviewMCP)
			}
			local := itemServers[root][server]
			if local == nil {
				local = &costOverviewMCP{Server: server}
				itemServers[root][server] = local
			}
			local.Calls += aggregate.Calls
			local.UnpricedCalls += aggregate.UnpricedCalls
			local.RecordedCostUSD += aggregate.RecordedCostUSD
		}
	}
	for _, user := range users {
		for _, work := range userWorks[user.ID] {
			user.ByWork = append(user.ByWork, work)
		}
		sort.Slice(user.ByWork, func(i, j int) bool {
			if user.ByWork[i].TotalCostUSD != user.ByWork[j].TotalCostUSD {
				return user.ByWork[i].TotalCostUSD > user.ByWork[j].TotalCostUSD
			}
			return user.ByWork[i].ID < user.ByWork[j].ID
		})
		resp.ByUser = append(resp.ByUser, user)
	}
	sort.Slice(resp.ByUser, func(i, j int) bool {
		if resp.ByUser[i].TotalCostUSD != resp.ByUser[j].TotalCostUSD {
			return resp.ByUser[i].TotalCostUSD > resp.ByUser[j].TotalCostUSD
		}
		return resp.ByUser[i].ID < resp.ByUser[j].ID
	})
	for _, bot := range bots {
		resp.ByBot = append(resp.ByBot, bot)
		if item := items[bot.Workflow]; item != nil {
			item.ByBot = append(item.ByBot, bot)
		}
	}
	sort.Slice(resp.ByBot, func(i, j int) bool {
		if resp.ByBot[i].TotalCostUSD != resp.ByBot[j].TotalCostUSD {
			return resp.ByBot[i].TotalCostUSD > resp.ByBot[j].TotalCostUSD
		}
		return resp.ByBot[i].ID < resp.ByBot[j].ID
	})
	for _, server := range servers {
		resp.ByMCP = append(resp.ByMCP, server)
	}
	sort.Slice(resp.ByMCP, func(i, j int) bool {
		if resp.ByMCP[i].Calls != resp.ByMCP[j].Calls {
			return resp.ByMCP[i].Calls > resp.ByMCP[j].Calls
		}
		return resp.ByMCP[i].Server < resp.ByMCP[j].Server
	})
	for _, item := range items {
		for _, actor := range itemUsers[item.ID] {
			item.ByUser = append(item.ByUser, actor)
		}
		sort.Slice(item.ByUser, func(i, j int) bool {
			if item.ByUser[i].TotalCostUSD != item.ByUser[j].TotalCostUSD {
				return item.ByUser[i].TotalCostUSD > item.ByUser[j].TotalCostUSD
			}
			return item.ByUser[i].ID < item.ByUser[j].ID
		})
		sort.Slice(item.ByBot, func(i, j int) bool {
			if item.ByBot[i].TotalCostUSD != item.ByBot[j].TotalCostUSD {
				return item.ByBot[i].TotalCostUSD > item.ByBot[j].TotalCostUSD
			}
			return item.ByBot[i].ID < item.ByBot[j].ID
		})
		for _, server := range itemServers[item.ID] {
			item.ByMCP = append(item.ByMCP, server)
		}
		sort.Slice(item.ByMCP, func(i, j int) bool {
			if item.ByMCP[i].Calls != item.ByMCP[j].Calls {
				return item.ByMCP[i].Calls > item.ByMCP[j].Calls
			}
			return item.ByMCP[i].Server < item.ByMCP[j].Server
		})
		resp.Items = append(resp.Items, item)
		resp.Total.Merge(item.Aggregate)
		mergeAggregateMap(resp.ByModel, item.ByModel)
		for _, aggregate := range item.ByModel {
			provider := aggregate.Provider
			if provider == "" {
				provider = "unknown"
			}
			bucket, ok := resp.ByProvider[provider]
			if !ok {
				bucket = &costledger.Aggregate{}
				resp.ByProvider[provider] = bucket
			}
			bucket.Merge(*aggregate)
		}
	}
	sort.Slice(resp.Items, func(i, j int) bool {
		if resp.Items[i].TotalCostUSD != resp.Items[j].TotalCostUSD {
			return resp.Items[i].TotalCostUSD > resp.Items[j].TotalCostUSD
		}
		return resp.Items[i].ID < resp.Items[j].ID
	})
	return resp
}

// handleCostOverview is GET /api/cost/overview. Optional `from` and `to`
// (YYYY-MM-DD, UTC) bound the range, as for /api/cost/summary.
func (api *StreamingAPI) handleCostOverview(w http.ResponseWriter, r *http.Request) {
	if api.costLedger == nil {
		http.Error(w, `{"error":"cost ledger not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	summary, err := api.costLedger.Summarize(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	visible := func(id, kind string) bool {
		if kind == costOverviewKindCrew {
			// Crew visibility is universal (see crew_directory.go).
			return true
		}
		if kind == costOverviewKindProduct {
			return costOverviewProductVisible(id, GetUserIDFromContext(r.Context()), currentUserIsAdmin(r))
		}
		return currentUserWorkflowAccess(r, id) != WorkflowAccessNone
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildCostOverview(summary, visible, currentUserIsAdmin(r)))
}
