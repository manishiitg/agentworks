package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

const dashboardQuestionRegistryPath = "_system/dashboard_questions/resources.json"

// As with the messaging store, mutations are serialized by the API process and
// persisted atomically by the workspace backend. These files are server-owned,
// never project evidence or a source of access grants.
var dashboardQuestionRegistryMu sync.Mutex

type dashboardQuestionDiscovery struct {
	Target   dashboardTarget
	Document dashboards.Dashboard
	Row      map[string]any
}

type dashboardQuestionResource struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	ProjectID string `json:"project_id"`
	Kind      string `json:"kind"`
	Dashboard string `json:"dashboard"`
	// Code is the short, unguessable name used in /d/<code> links. It names the
	// dashboard only; opening it still needs a signed-in person with access.
	Code string `json:"code,omitempty"`
}

const dashboardShortCodeAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

func newDashboardShortCode(taken map[string]bool) string {
	for {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			continue
		}
		out := make([]byte, len(raw))
		for i, b := range raw {
			out[i] = dashboardShortCodeAlphabet[int(b)%len(dashboardShortCodeAlphabet)]
		}
		if code := string(out); !taken[code] {
			return code
		}
	}
}

type dashboardQuestionRegistry struct {
	Resources []dashboardQuestionResource `json:"resources"`
}

func readDashboardQuestionJSON(ctx context.Context, p string, out any) error {
	raw, exists, err := readFileFromWorkspace(ctx, p)
	if err != nil || !exists {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

func saveDashboardQuestionJSON(ctx context.Context, p string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, p, string(raw))
}

// Called only after ordinary dashboard authorization. A stable random resource
// id names a project/dashboard pair; it never conveys sharing permission.
func registerDashboardQuestionResources(ctx context.Context, items []dashboardQuestionDiscovery) ([]string, error) {
	ids, _, err := registerDashboardQuestionResourcesWithCodes(ctx, items)
	return ids, err
}

// Same registration, also returning each resource's short link code (created
// on first use, including for resources registered before codes existed).
func registerDashboardQuestionResourcesWithCodes(ctx context.Context, items []dashboardQuestionDiscovery) ([]string, []string, error) {
	dashboardQuestionRegistryMu.Lock()
	defer dashboardQuestionRegistryMu.Unlock()
	var registry dashboardQuestionRegistry
	if err := readDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, &registry); err != nil {
		return nil, nil, err
	}
	type key struct{ root, project, kind, dashboard string }
	ids := make(map[key]int, len(registry.Resources))
	taken := make(map[string]bool, len(registry.Resources))
	for i, resource := range registry.Resources {
		ids[key{resource.Root, resource.ProjectID, resource.Kind, resource.Dashboard}] = i
		if resource.Code != "" {
			taken[resource.Code] = true
		}
	}
	result := make([]string, len(items))
	codes := make([]string, len(items))
	changed := false
	for i, item := range items {
		if !item.Document.Managed {
			continue
		}
		target, slug := item.Target, item.Document.ID
		k := key{target.Root, target.ID, target.Kind, slug}
		at, found := ids[k]
		if !found {
			if len(registry.Resources) >= 10000 {
				return nil, nil, dashboardFailure(503, "dashboard resource registry is full")
			}
			code := newDashboardShortCode(taken)
			taken[code] = true
			registry.Resources = append(registry.Resources, dashboardQuestionResource{ID: uuid.NewString(), Root: target.Root, ProjectID: target.ID, Kind: target.Kind, Dashboard: slug, Code: code})
			at, changed = len(registry.Resources)-1, true
			ids[k] = at
		} else if registry.Resources[at].Code == "" {
			code := newDashboardShortCode(taken)
			taken[code] = true
			registry.Resources[at].Code, changed = code, true
		}
		result[i], codes[i] = registry.Resources[at].ID, registry.Resources[at].Code
	}
	if changed {
		if err := saveDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, registry); err != nil {
			return nil, nil, err
		}
	}
	return result, codes, nil
}

func addDashboardQuestionResource(ctx context.Context, target dashboardTarget, document dashboards.Dashboard, row map[string]any) {
	// Legacy loose HTML has no immutable publication history. Keep its current
	// discovery behavior; the question contract starts with managed bundles.
	if !document.Managed {
		return
	}
	addDashboardQuestionResources(ctx, []dashboardQuestionDiscovery{{target, document, row}})
}

func addDashboardQuestionResources(ctx context.Context, items []dashboardQuestionDiscovery) {
	managed := false
	for _, item := range items {
		managed = managed || item.Document.Managed
	}
	if !managed {
		return
	}
	ids, codes, err := registerDashboardQuestionResourcesWithCodes(ctx, items)
	if err != nil {
		// Question availability must not become a dependency of ordinary
		// dashboard discovery. No id means the caller cannot offer Ask.
		log.Printf("[DASHBOARD QUESTIONS] Resource registration unavailable: %v", err)
		return
	}
	for i, id := range ids {
		if id != "" {
			items[i].Row["resource_id"] = id
			if codes[i] != "" {
				items[i].Row["short_path"] = "/d/" + codes[i]
				if base := effectiveShareBaseURL(); base != "" && items[i].Row["url"] != nil {
					items[i].Row["short_url"] = strings.TrimRight(base, "/") + "/d/" + codes[i]
				}
			}
		}
	}
}
