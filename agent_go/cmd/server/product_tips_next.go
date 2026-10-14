package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Working tips are chosen here, never in the browser. After a turn has worked for ten seconds the app asks for the
// one tip to show under the working line; the server picks it, records that it was shown and remembers the answer for
// the rest of that turn. A person can turn tips off. The browser only reports facts only it knows (how many Code tabs
// are open, which features this browser has already used) and shows what comes back.

// A tip that was shown stays off the list for this long, unless every tip has been shown (then the oldest comes back
// after the cooldown). "A few days" in the owner's words.
const productTipCooldown = 72 * time.Hour

type productTipsState struct {
	// Disabled is the person's own choice; absent means tips are on.
	Disabled bool `json:"disabled,omitempty"`
	// Shown maps tip id to the last time it was shown to this person.
	Shown map[string]string `json:"shown,omitempty"`
	// LastTurn and LastTip make a repeated request for the same turn return the same tip without using up another.
	LastTurn string `json:"last_turn,omitempty"`
	LastTip  string `json:"last_tip,omitempty"`
}

type productTipContext struct {
	Surface  string
	CodeTabs int
	Used     map[string]bool
}

func productTipEligible(tip agentprofiles.WorkingTip, context productTipContext) bool {
	if tip.SkipIfUsed != "" && context.Used[tip.SkipIfUsed] {
		return false
	}
	if tip.When != nil && tip.When.CodeTabs != nil {
		if min := tip.When.CodeTabs.Min; min != nil && context.CodeTabs < *min {
			return false
		}
		if max := tip.When.CodeTabs.Max; max != nil && context.CodeTabs > *max {
			return false
		}
	}
	return true
}

// selectProductTip picks the tip for one turn: a tip this person has never seen first (in the YAML order), otherwise
// the one shown longest ago whose cooldown has passed; none when every eligible tip is still cooling down. Pure, so the
// rule is testable without a server.
func selectProductTip(tips []agentprofiles.WorkingTip, context productTipContext, state productTipsState, now time.Time) *agentprofiles.WorkingTip {
	type candidate struct {
		tip   agentprofiles.WorkingTip
		shown time.Time
	}
	var unseen, cooled []candidate
	for _, tip := range tips {
		if strings.TrimSpace(tip.Text) == "" || !productTipEligible(tip, context) {
			continue
		}
		raw, seen := state.Shown[tip.ID]
		if !seen {
			unseen = append(unseen, candidate{tip: tip})
			continue
		}
		shown, err := time.Parse(time.RFC3339, raw)
		if err != nil || now.Sub(shown) >= productTipCooldown {
			cooled = append(cooled, candidate{tip: tip, shown: shown})
		}
	}
	if len(unseen) > 0 {
		return &unseen[0].tip
	}
	if len(cooled) > 0 {
		sort.SliceStable(cooled, func(i, j int) bool { return cooled[i].shown.Before(cooled[j].shown) })
		return &cooled[0].tip
	}
	return nil
}

func productTipsStatePath(userID string) string {
	return filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(userID, "preferences"), "product-tips.json"))
}

func readProductTipsState(r *http.Request, path string) (productTipsState, error) {
	var state productTipsState
	raw, exists, err := readFileFromWorkspace(r.Context(), path)
	if err != nil {
		return state, err
	}
	if exists && strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &state) // an unreadable file behaves as an empty one
	}
	if state.Shown == nil {
		state.Shown = map[string]string{}
	}
	return state, nil
}

func writeProductTipsState(r *http.Request, path string, state productTipsState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeRawFileToWorkspace(r.Context(), path, string(data))
}

// GET /api/product-tips/next?surface=code&code_tabs=2&used=quick-switcher,chat-tab-new&turn=<key>
// Answers {"tip": {"id","text"}} or {"tip": null}, and records a shown tip.
func (api *StreamingAPI) handleProductTipNext(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tip": nil})
		return
	}
	query := r.URL.Query()
	surface := strings.TrimSpace(query.Get("surface"))
	catalog, err := cachedProductTips()
	if err != nil {
		http.Error(w, "Product tips are unavailable", http.StatusInternalServerError)
		return
	}
	tips, known := catalog[surface]
	none := func() {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"tip": nil})
	}
	if !known || !productpolicy.Enabled(surface) || !userAllowedProduct(claims, surface) {
		none()
		return
	}
	context := productTipContext{Surface: surface, CodeTabs: 1, Used: map[string]bool{}}
	if tabs, err := strconv.Atoi(query.Get("code_tabs")); err == nil && tabs > 0 {
		context.CodeTabs = tabs
	}
	for _, feature := range strings.Split(query.Get("used"), ",") {
		if feature = strings.TrimSpace(feature); feature != "" {
			context.Used[feature] = true
		}
	}
	turn := strings.TrimSpace(query.Get("turn"))
	path := productTipsStatePath(claims.UserID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	state, err := readProductTipsState(r, path)
	if err != nil {
		none() // a tip is never worth failing a turn's screen
		return
	}
	if state.Disabled {
		none()
		return
	}
	// The same turn asking again (a reload, a second tab) gets the tip it already has.
	if turn != "" && state.LastTurn == turn {
		for _, tip := range tips {
			if tip.ID == state.LastTip {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				_ = json.NewEncoder(w).Encode(map[string]any{"tip": map[string]string{"id": tip.ID, "text": tip.Text}})
				return
			}
		}
		none()
		return
	}
	pick := selectProductTip(tips, context, state, time.Now().UTC())
	if pick == nil {
		none()
		return
	}
	state.Shown[pick.ID] = time.Now().UTC().Format(time.RFC3339)
	// Keep the file bounded: forget tips that are no longer in any surface's catalog.
	inCatalog := map[string]bool{}
	for _, surfaceTips := range catalog {
		for _, tip := range surfaceTips {
			inCatalog[tip.ID] = true
		}
	}
	for id := range state.Shown {
		if !inCatalog[id] {
			delete(state.Shown, id)
		}
	}
	state.LastTurn, state.LastTip = turn, pick.ID
	if err := writeProductTipsState(r, path, state); err != nil {
		none()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"tip": map[string]string{"id": pick.ID, "text": pick.Text}})
}

// GET/PUT /api/product-tips/preference {"enabled": bool}: the person's own on/off switch for working tips.
func (api *StreamingAPI) handleProductTipPreference(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to change tips", http.StatusUnauthorized)
		return
	}
	path := productTipsStatePath(claims.UserID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	state, err := readProductTipsState(r, path)
	if err != nil {
		http.Error(w, "Tips settings are unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Enabled == nil {
			http.Error(w, "enabled is required", http.StatusBadRequest)
			return
		}
		state.Disabled = !*body.Enabled
		if err := writeProductTipsState(r, path, state); err != nil {
			http.Error(w, "Could not save the setting", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"enabled": !state.Disabled})
}
