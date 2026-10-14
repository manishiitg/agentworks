package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/guidancesessions"
)

type externalGuidanceHandleKey struct{}

// Topics belong to canonical operations. Every adapter resolves merged actions
// before this lookup. Further products can add topics without changing sessions.
func externalToolGuidance(name string) []string {
	if isDashboardTool(name) && dashboardActions[name] != "list" && dashboardActions[name] != "get" && dashboardActions[name] != "link" {
		return []string{"dashboard-authoring"}
	}
	return nil
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
	schemas, _ := value["schemas"].(map[string]any)
	for name := range schemas {
		topics = append(topics, externalToolGuidance(name)...)
		if name == "dashboard" {
			definition, _ := schemas[name].(map[string]any)
			schema, _ := definition["inputSchema"].(map[string]any)
			properties, _ := schema["properties"].(map[string]any)
			action, _ := properties["action"].(map[string]any)
			raw, _ := json.Marshal(action["enum"])
			var actions []string
			_ = json.Unmarshal(raw, &actions)
			for _, item := range actions {
				if item != "list" && item != "get" && item != "link" {
					topics = append(topics, "dashboard-authoring")
					break
				}
			}
		}
	}
	versions := externalGuidanceVersions(GetUserFromContext(r.Context()), topics)
	if len(versions) == 0 {
		return
	}
	receipt := api.guidanceReceipt(r, handle, true, versions, nil, false)
	files := []map[string]any{}
	delivered := map[string]bool{}
	for _, item := range receipt.Topics {
		if item.Status == "delivered_to_client" {
			continue
		}
		topic := externalGuidanceTopicByName(item.Name)
		body, err := externalGuidanceSkillContent(GetUserFromContext(r.Context()), *topic)
		if err != nil {
			continue
		}
		files = append(files, map[string]any{"name": item.Name, "version": item.Version, "files": []any{map[string]any{"path": "SKILL.md", "content": body}}})
		delivered[item.Name] = true
	}
	if len(delivered) > 0 {
		receipt = api.guidanceReceipt(r, receipt.SessionID, false, versions, delivered, false)
	}
	value["guidance"] = receipt
	if len(files) > 0 {
		value["guidance_skills"] = files
	}
}

// One response decorator covers REST, CLI, stdio and remote MCP execution.
// Existing fields and status codes survive; ordinary operations never get bodies.
func (api *StreamingAPI) externalGuidanceResponse(w http.ResponseWriter, r *http.Request, name, handle string) (http.ResponseWriter, func()) {
	rec := &externalMCPRecorder{header: http.Header{}}
	return rec, func() {
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(rec.body.Bytes()))
		decoder.UseNumber() // preserve domain IDs and counters above 2^53
		if decoder.Decode(&value) == nil && value != nil {
			topics := externalToolGuidance(name)
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
			if len(versions) > 0 {
				failure, _ := value["error"].(map[string]any)
				receipt := api.guidanceReceipt(r, handle, len(delivered) > 0, versions, delivered, failure["code"] == "invalid_arguments" || failure["code"] == "invalid_dashboard" || value["valid"] == false)
				if name == "get_guidance_topic" || name == "get_guidance_skills" {
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
