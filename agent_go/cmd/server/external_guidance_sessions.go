package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/guidancesessions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
)

type externalGuidanceHandleKey struct{}

// Topics belong to canonical operations. Every adapter resolves merged actions
// before this lookup. Further products can add topics without changing sessions.
func externalToolGuidance(name string) []string {
	var names []string
	for _, topic := range externalGuidanceTopics {
		for _, pattern := range topic.Tools {
			if matched, _ := path.Match(pattern, name); matched {
				names = append(names, topic.Name)
				break
			}
		}
	}
	if len(names) == 0 {
		names = []string{productguide.SkillName}
	}
	return names
}

func externalToolGuidanceFor(claims *UserClaims, name string) []string {
	var names []string
	for _, topic := range externalToolGuidance(name) {
		if source := externalGuidanceTopicByName(topic); source != nil && externalTopicVisible(claims, *source) {
			names = append(names, topic)
		}
	}
	if len(names) == 0 {
		names = []string{productguide.SkillName}
	}
	return names
}

// Schemas use only this connection's allowed members, including hidden aliases.
// Related operations share a skill; an unrelated tool uses the common MCP guide.
func externalSchemaGuidance(claims *UserClaims, name string) []string {
	tool, ok := externalCatalogTool(name)
	if !ok || tool.actions == nil {
		return externalToolGuidanceFor(claims, name)
	}
	seen := map[string]bool{}
	for _, spec := range tool.actions {
		for _, memberName := range externalMemberNames(spec) {
			if member, found := externalCatalogTool(memberName); found && externalTokenAllows(claims, member) {
				for _, topic := range externalToolGuidanceFor(claims, memberName) {
					seen[topic] = true
				}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for topic := range seen {
		names = append(names, topic)
	}
	sort.Strings(names)
	return names
}

func externalOperationHasGuidance(tool externalTool, args map[string]any) bool {
	if isDashboardTool(tool.Name) && dashboardActions[tool.Name] != "list" && dashboardActions[tool.Name] != "get" && dashboardActions[tool.Name] != "link" {
		return dashboardActions[tool.Name] != "validate" && dashboardActions[tool.Name] != "preview"
	}
	if tool.Name == "call_crew_function" || tool.Name == "ask_crew" {
		return true
	}
	if !tool.mutates && !tool.executes {
		return false
	}
	// Compound tools keep their native action schemas. Their inspection actions
	// do not need per-call reminders even when other actions change state.
	action := externalArg(args, "action")
	for _, prefix := range []string{"list_", "get_", "read_", "search_", "inspect_"} {
		if strings.HasPrefix(action, prefix) {
			return false
		}
	}
	switch action {
	case "list", "get", "read", "search", "browse", "inspect", "status", "history", "runs", "calls", "catalog", "discover", "diagnostics", "validate", "preview", "usage", "export", "check", "backup_status", "secrets_list", "schedule_status", "whats_new":
		return false
	}
	return true
}

func externalGuidanceCaller(r *http.Request) string {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		return ""
	}
	connection := r.Header.Get("Authorization")
	if claims.AccessToken != nil {
		connection = claims.AccessToken.ID
	} else if connection == "" {
		connection = r.URL.Query().Get("token")
	}
	// Do not persist credentials, or collapse unrelated browser connections.
	sum := sha256.Sum256([]byte(connection))
	return claims.Provider + "\x00" + claims.UserID + "\x00" + hex.EncodeToString(sum[:])
}

func (api *StreamingAPI) guidanceReceipt(r *http.Request, handle string, create bool, versions map[string]string, delivered map[string]bool, failed bool) guidancesessions.Receipt {
	// One SQLite connection and this mutex serialize short receipt transactions.
	// Guidance remains advisory; this intentionally favors simple durable state
	// over parallel receipt writes in the first slice.
	api.guidanceStoreMu.Lock()
	defer api.guidanceStoreMu.Unlock()
	path, err := privateAuthorityStatePath("-guidance.sqlite")
	if err == nil && (api.guidanceStore == nil || api.guidanceStorePath != path) {
		if api.guidanceStore != nil {
			_ = api.guidanceStore.Close()
			api.guidanceStore = nil
		}
		api.guidanceStore, err = guidancesessions.Open(path)
		api.guidanceStorePath = path
	}
	var receipt guidancesessions.Receipt
	if err == nil {
		receipt, err = api.guidanceStore.Check(r.Context(), externalGuidanceCaller(r), handle, create, versions, delivered, failed, time.Now())
	}
	if err != nil {
		// Advisory state errors never fail or change the product operation.
		receipt = guidancesessions.Receipt{Advisory: true, Topics: []guidancesessions.Topic{}, Fetch: "Guidance tracking unavailable. Fetch relevant full skills with help action=skills; current permissions still apply."}
		for name, version := range versions {
			receipt.Topics = append(receipt.Topics, guidancesessions.Topic{Name: name, Version: version, Status: "unavailable"})
		}
	}
	sort.Slice(receipt.Topics, func(i, j int) bool { return receipt.Topics[i].Name < receipt.Topics[j].Name })
	return receipt
}

func externalGuidanceVersions(claims *UserClaims, names []string) map[string]string {
	versions := map[string]string{}
	for _, name := range names {
		topic := externalGuidanceTopicByName(name)
		if topic != nil && externalTopicVisible(claims, *topic) {
			versions[topic.Name] = externalTopicVersionFor(claims, *topic)
		}
	}
	return versions
}

// Selected schema preparation serves full canonical skills once per delivered
// topic/version. Metadata discovery never creates a delivery record. An explicit
// full help fetch ignores the receipt, so a compacted context can reload it.
func (api *StreamingAPI) externalPrepareGuidance(r *http.Request, value map[string]any, handle string) {
	topics := []string{}
	requirements := map[string][]string{}
	schemas, _ := value["schemas"].(map[string]any)
	for name := range schemas {
		requirements[name] = externalSchemaGuidance(GetUserFromContext(r.Context()), name)
		topics = append(topics, requirements[name]...)
	}
	versions := externalGuidanceVersions(GetUserFromContext(r.Context()), topics)
	if len(versions) == 0 {
		return
	}
	receipt := api.guidanceReceipt(r, handle, false, versions, nil, false)
	allowFallback := receipt.SessionID != ""
	if receipt.SessionID == "" {
		for topic := range versions {
			if topic != productguide.SkillName {
				receipt = api.guidanceReceipt(r, handle, true, versions, nil, false)
				break
			}
		}
	}
	files := []map[string]any{}
	delivered := map[string]bool{}
	for _, item := range receipt.Topics {
		if item.Name == productguide.SkillName && !allowFallback {
			continue
		}
		if item.Status == "delivered_to_client" {
			continue
		}
		topic := externalGuidanceTopicByName(item.Name)
		body, err := externalGuidanceSkillFiles(GetUserFromContext(r.Context()), *topic)
		if err != nil {
			continue
		}
		files = append(files, map[string]any{"name": item.Name, "version": item.Version, "files": body})
		delivered[item.Name] = true
	}
	if len(delivered) > 0 {
		receipt = api.guidanceReceipt(r, receipt.SessionID, false, versions, delivered, false)
	}
	value["guidance"] = receipt
	value["guidance_requirements"] = requirements
	if len(files) > 0 {
		value["guidance_skills"] = files
	}
}

// One response decorator covers REST, CLI, stdio and remote MCP execution.
// Existing fields and status codes survive; ordinary operations never get bodies.
func (api *StreamingAPI) externalGuidanceResponse(w http.ResponseWriter, r *http.Request, name, handle string, arguments ...map[string]any) (http.ResponseWriter, func()) {
	var args map[string]any
	if len(arguments) > 0 {
		args = arguments[0]
	}
	rec := &externalMCPRecorder{header: http.Header{}}
	return rec, func() {
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(rec.body.Bytes()))
		decoder.UseNumber() // preserve domain IDs and counters above 2^53
		if decoder.Decode(&value) == nil && value != nil {
			topics := externalToolGuidanceFor(GetUserFromContext(r.Context()), name)
			if name == "get_guidance_topic" || name == "get_guidance_skills" || externalProjectSkillFetch(name, args) {
				topics = nil
			}
			delivered := map[string]bool{}
			if rec.status >= 200 && rec.status < 300 && name == "get_guidance_topic" {
				topic, _ := value["topic"].(string)
				content, _ := value["content"].(string)
				if content != "" {
					if canonical := externalGuidanceTopicByName(topic); canonical != nil {
						topics = append(topics, canonical.Name)
						delivered[canonical.Name] = true
					}
				}
			}
			if rec.status >= 200 && rec.status < 300 && name == "get_guidance_skills" {
				skills, _ := value["skills"].([]any)
				for _, raw := range skills {
					item, _ := raw.(map[string]any)
					topic, _ := item["name"].(string)
					files, _ := item["files"].([]any)
					for _, rawFile := range files {
						file, _ := rawFile.(map[string]any)
						content, _ := file["content"].(string)
						path, _ := file["path"].(string)
						if content != "" && path == "SKILL.md" {
							topics = append(topics, topic)
							delivered[topic] = true
						}
					}
				}
			}
			versions := externalGuidanceVersions(GetUserFromContext(r.Context()), topics)
			var resource map[string]any
			if rec.status >= 200 && rec.status < 300 && value["error"] == nil {
				if topic, version, source := externalDeliveredProjectSkill(name, args, value); topic != "" {
					versions[topic], delivered[topic], resource = version, true, source
				}
			}
			if len(versions) > 0 {
				failure, _ := value["error"].(map[string]any)
				receipt := api.guidanceReceipt(r, handle, len(delivered) > 0, versions, delivered, failure["code"] == "invalid_arguments" || failure["code"] == "invalid_dashboard" || value["valid"] == false)
				if resource != nil {
					receipt.Fetch = "Reuse this delivered skill in this conversation. Fetch it again through the original authorized skill/package or file tool after context loss or a version change."
					value["guidance_resources"] = []any{resource}
				}
				if name == "get_guidance_topic" || name == "get_guidance_skills" || resource != nil {
					value["guidance"] = receipt
				} else {
					value["guidance"] = externalCompactGuidance(receipt)
				}
				if data, err := json.Marshal(value); err == nil {
					rec.header.Del("Content-Length")
					rec.body.Reset()
					rec.body.Write(data)
					rec.body.WriteByte('\n')
				}
			}
		}
		for key, values := range rec.header {
			w.Header()[key] = values
		}
		if rec.status != 0 {
			w.WriteHeader(rec.status)
		}
		_, _ = w.Write(rec.body.Bytes())
	}
}

func externalWithGuidanceHandle(r *http.Request, handle string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), externalGuidanceHandleKey{}, handle))
}

func (api *StreamingAPI) closeGuidanceSessions() {
	api.guidanceStoreMu.Lock()
	defer api.guidanceStoreMu.Unlock()
	if api.guidanceStore != nil {
		_ = api.guidanceStore.Close()
		api.guidanceStore = nil
	}
}

// Ordinary authoring replies need only a topic/version/status. Handles and
// timestamps belong in preparation, not twenty repeated operation responses.
func externalCompactGuidance(receipt guidancesessions.Receipt) string {
	if len(receipt.Topics) > 1 {
		for _, topic := range receipt.Topics {
			if topic.Status != "delivered_to_client" {
				return topic.Status + " get_guidance_skills"
			}
		}
		return "skills current"
	}
	parts := make([]string, 0, len(receipt.Topics))
	for _, topic := range receipt.Topics {
		status := topic.Status
		if status == "delivered_to_client" {
			status = "current"
		}
		hint := topic.Name + "@" + topic.Version + " " + status
		if status != "current" {
			hint += " get_guidance_skills"
		}
		parts = append(parts, hint)
	}
	return strings.Join(parts, "; ")
}
