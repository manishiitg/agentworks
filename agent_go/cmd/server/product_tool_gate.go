package server

import (
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// productToolGate is the single place a product profile decides which tools its
// agent receives.
//
// It is installed once, on the agent wrapper's one registration chokepoint
// (LLMAgentWrapper.RegisterCustomToolWithTimeout), rather than at each of the
// ~14 call sites that register tools. That matters: those sites are spread
// across platform pools, secret tools, workflow tools, and product-declared
// tools, and their separate policies drifted apart three times. One chokepoint
// cannot be bypassed by a path nobody remembered to update.
//
// Two things it deliberately does not do:
//
//   - It never filters after launch. Registration completes before the coding
//     CLI caches its catalog via get_api_spec, so anything dropped here is
//     dropped before the agent could have discovered it. Nothing is hidden from
//     a running agent.
//   - It carries no per-turn or per-mode view. Mode discipline belongs in the
//     system prompt; code enforces authority, not focus.
//
// See docs/design/agent_tool_surface_single_source.md.
type productToolGate struct {
	profileID             string
	workflowNotifications bool
	deny                  func(string) bool

	// allowed is nil in observe mode: every tool passes and is recorded, so a
	// real enabled: list can be seeded from a live session instead of guessed.
	allowed map[string]struct{}

	// readerDenied overlays a deny-list for read-only turns (Crew Run
	// mode): mutating tools are dropped at the same chokepoint even when
	// a registration path admits them. Authority, not focus: readers
	// must not mutate, whatever the prompt says.
	readerDenied map[string]struct{}

	// shadow is the product.yaml list a chat would be held to once it enforces (PLAT-608 step 4). It filters
	// nothing; a registered tool outside it is recorded in wouldFilter and logged, so the list can be completed from
	// live sessions before the gate is switched to allowlist.
	shadow      map[string]struct{}
	wouldFilter []string

	mu         sync.Mutex
	registered []string
	filtered   []string
}

// Shadow names the chat surface and the product.yaml list it is measured against, without filtering anything.
func (g *productToolGate) Shadow(surface string, names []string) {
	if g == nil || g.enforcing() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.profileID == "" {
		g.profileID = surface
	}
	g.shadow = make(map[string]struct{}, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			g.shadow[name] = struct{}{}
		}
	}
}

// newProductToolGate builds a gate for a resolved profile. A nil profile, or a
// profile without tool_policy.mode=allowlist, yields observe mode: no product tools are
// filtered except workflow-only notifications, and every admitted name is recorded.
func newProductToolGate(resolved *resolvedAgentProfile) *productToolGate {
	gate := &productToolGate{}
	if resolved == nil {
		return gate
	}
	gate.profileID = resolved.Definition.ID
	policy := resolved.Definition.ToolPolicy
	if !policy.IsAllowlist() {
		return gate
	}
	gate.allowed = make(map[string]struct{}, len(policy.Enabled))
	for _, name := range policy.Enabled {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			gate.allowed[trimmed] = struct{}{}
		}
	}
	return gate
}

// newProductToolGateForAllowlist applies a product chat surface through the
// same registration chokepoint used by profile tool policies.
func newProductToolGateForAllowlist(productID string, names []string) *productToolGate {
	gate := &productToolGate{profileID: productID, allowed: make(map[string]struct{}, len(names))}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			gate.allowed[name] = struct{}{}
		}
	}
	return gate
}

// enforcing reports whether the gate filters. False means observe mode.
func (g *productToolGate) enforcing() bool { return g != nil && g.allowed != nil }

// Declare admits the public name produced by a tool binding in profile.tools.
//
// A binding names its registered factory (for example "work.set-identity"),
// while the factory owns the public tool name exposed to the model (for
// example "set_work_identity"). Requiring that second name to also be copied
// into tool_policy.enabled creates two sources of truth and lets them drift.
// The binding is already an explicit product capability declaration, so once
// its registered factory has resolved successfully, its public name belongs
// to the same effective allowlist as feature-projected tools.
func (g *productToolGate) Declare(name string) {
	if g == nil {
		return
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.allowed == nil {
		return
	}
	g.allowed[trimmed] = struct{}{}
	// If another registration path attempted this public name before its
	// profile binding was built, the final diagnostic should reflect the
	// effective surface rather than retain a stale filtered entry.
	kept := g.filtered[:0]
	for _, filtered := range g.filtered {
		if strings.TrimSpace(filtered) != trimmed {
			kept = append(kept, filtered)
		}
	}
	g.filtered = kept
}

// DenyReaderTools arms the read-only overlay: every named tool is
// refused at admission for the rest of the turn.
func (g *productToolGate) DenyReaderTools(names ...string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.readerDenied == nil {
		g.readerDenied = map[string]struct{}{}
	}
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			g.readerDenied[trimmed] = struct{}{}
		}
	}
}

// DenyWhere adds an authority boundary to every registration path, including
// connected MCP and the coding-agent bridge, independently of profile focus.
func (g *productToolGate) DenyWhere(denied func(string) bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.deny = denied
}

// Allows reports the current policy without recording a registration attempt.
// Prompt assembly uses this to describe only tools the agent can actually see.
func (g *productToolGate) Allows(name string) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.allowsLocked(strings.TrimSpace(name))
}

// AllowWorkflowNotifications enables the workflow-only notification capability.
// Product profiles remain excluded even if an allowlist or tool factory declares it.
func (g *productToolGate) AllowWorkflowNotifications(enabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.workflowNotifications = enabled && g.profileID == ""
}

func (g *productToolGate) allowsLocked(name string) bool {
	if name == "notify_user" && !g.workflowNotifications {
		return false
	}
	if g.deny != nil && g.deny(name) {
		return false
	}
	if _, denied := g.readerDenied[name]; denied {
		return false
	}
	if g.allowed != nil {
		_, ok := g.allowed[name]
		return ok
	}
	return true
}

// Admit is the hook handed to the agent wrapper. It is called while the wrapper
// holds its own lock, so it must never call back into the wrapper.
func (g *productToolGate) Admit(name string) bool {
	if g == nil {
		return true
	}
	trimmed := strings.TrimSpace(name)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.allowsLocked(trimmed) {
		g.filtered = append(g.filtered, trimmed)
		return false
	}
	g.registered = append(g.registered, trimmed)
	if g.shadow != nil {
		if _, listed := g.shadow[trimmed]; !listed {
			g.wouldFilter = append(g.wouldFilter, trimmed)
		}
	}
	return true
}

// summary returns the sorted, de-duplicated names admitted and dropped.
func (g *productToolGate) summary() (registered, filtered []string) {
	if g == nil {
		return nil, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return uniqueSortedToolNames(g.registered), uniqueSortedToolNames(g.filtered)
}

// logSurface records the session's effective tool surface. An allowlist fails
// closed, so a capability the agent turns out to be missing must be
// diagnosable from the log rather than from confused agent behavior. In observe
// mode the registered list is exactly what tool_policy.enabled should contain
// to preserve current behavior.
func (g *productToolGate) logSurface(sessionID string) {
	if g == nil || g.profileID == "" {
		return
	}
	registered, filtered := g.summary()
	mode := "observe"
	if g.enforcing() {
		mode = agentprofiles.ToolPolicyModeAllowlist
	}
	log.Printf("[PRODUCT_TOOL_GATE] profile=%s session=%s mode=%s registered=%d: %s",
		g.profileID, sessionID, mode, len(registered), strings.Join(registered, " "))
	if len(filtered) > 0 {
		log.Printf("[PRODUCT_TOOL_GATE] profile=%s session=%s filtered=%d: %s",
			g.profileID, sessionID, len(filtered), strings.Join(filtered, " "))
	}
	g.mu.Lock()
	wouldFilter := uniqueSortedToolNames(g.wouldFilter)
	shadowed := g.shadow != nil
	g.mu.Unlock()
	if shadowed {
		log.Printf("[PRODUCT_TOOL_GATE] profile=%s session=%s would_filter=%d: %s",
			g.profileID, sessionID, len(wouldFilter), strings.Join(wouldFilter, " "))
	}
}

func uniqueSortedToolNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
