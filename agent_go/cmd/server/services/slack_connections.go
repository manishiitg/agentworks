package services

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"strings"
)

// SlackConnection is one Slack app identity (bot token + app token pair) the
// server can talk to. The platform keeps a registry of connections in
// slack-config.json; each workflow may select its own via
// WorkflowCapabilities.SlackConnectionID, while anything without a selection
// uses the default connection. A connection scoped to a workflow
// (WorkspacePath != "") is managed by that workflow's owners; unscoped
// connections, including the default, are managed by platform admins.
type SlackConnection struct {
	// ID is stable for the life of the connection and is what workflow config
	// references. An identifier, never a secret.
	ID string `json:"id"`
	// DisplayName is the human label shown in the UI ("Support", "Ops").
	DisplayName string `json:"display_name"`
	// BotToken is the Bot User OAuth Token (xoxb-...). Encrypted at rest,
	// masked on read.
	BotToken string `json:"bot_token"`
	// AppToken is the App-Level Token (xapp-...) for Socket Mode. Encrypted
	// at rest, masked on read.
	AppToken string `json:"app_token"`
	// Enabled gates this connection's Socket Mode listener. A disabled
	// connection fails sends; it never falls through to another identity.
	Enabled bool `json:"enabled"`
	// WorkspacePath scopes the connection to the owning workflow or product
	// project. Empty means platform-managed (admins only).
	WorkspacePath string `json:"workspace_path,omitempty"`
	// ProfileID names the agent profile for product-scoped connections
	// (crew projects). Empty for workflow and platform scopes.
	ProfileID string `json:"profile_id,omitempty"`
	// ChannelRoutes lets the owner of a scoped connection share it with
	// other workflows or crew projects they can write: a channel listed here
	// is answered by that route's destination instead of the connection's
	// own. Keyed by Slack channel ID; one destination per channel. Ignored
	// on platform (unscoped) connections, which route through the shared
	// bot's channel routes instead.
	ChannelRoutes map[string]SlackConnectionRoute `json:"channel_routes,omitempty"`
	// Targets are more workflows, Crews and Codes its owner attached to this
	// bot (PLAT-668). Each is reachable by its slug in a 1:1 DM, with the
	// sender's own access; Codes answer DMs only. Channels list theirs in
	// ChannelRoutes.
	Targets []SlackTargetRef `json:"targets,omitempty"`
}

// SlackConnectionRoute is one channel route on a scoped connection. It names
// a destination only; the server resolves the workflow ID or crew
// conversation from it on every message, the same way it resolves the
// connection's own destination.
type SlackConnectionRoute struct {
	// WorkspacePath is the destination workflow folder or crew project.
	WorkspacePath string `json:"workspace_path"`
	// ProfileID names the agent profile for crew destinations; empty for
	// workflows.
	ProfileID string `json:"profile_id,omitempty"`
	// AddedBy records who created the route, for audit.
	AddedBy string `json:"added_by,omitempty"`
	// Targets are more targets allowed in this channel, picked with
	// "@bot <slug>" (PLAT-668). The destination above, when set, is the
	// channel's default; with no destination the bot asks with buttons. A
	// route saved before slugs is a one-target list that is also the
	// default.
	Targets []SlackTargetRef `json:"targets,omitempty"`
	// Trigger runs the default target's automation on top-level messages in
	// this channel, as on the shared bot's routes (PLAT-668).
	Trigger *SlackTrigger `json:"trigger,omitempty"`
}

// Default names the channel's default target, or an empty ref.
func (r SlackConnectionRoute) Default() SlackTargetRef {
	return SlackTargetRef{WorkspacePath: strings.TrimSpace(r.WorkspacePath), ProfileID: strings.TrimSpace(r.ProfileID)}
}

// Allowed lists every target allowed in the channel, default first.
func (r SlackConnectionRoute) Allowed() []SlackTargetRef {
	var out []SlackTargetRef
	if def := r.Default(); !def.Empty() {
		out = append(out, def)
	}
	for _, target := range r.Targets {
		if target.Empty() {
			continue
		}
		duplicate := false
		for _, seen := range out {
			if seen.Same(target) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, target)
		}
	}
	return out
}

// SameDestination reports whether the route points at the given scope.
func (r SlackConnectionRoute) SameDestination(workspacePath, profileID string) bool {
	return SameSlackScopePath(r.WorkspacePath, workspacePath) &&
		strings.TrimSpace(r.ProfileID) == strings.TrimSpace(profileID)
}

// SameSlackScopePath reports whether two scope paths name the same folder. A
// crew has a logical form ("Chats/Work/projects/<id>", the owner's own) and a
// physical one ("_users/<owner>/Chats/Work/projects/<id>"); a logical path
// equals the physical path with the same suffix. Two physical paths must match
// exactly, so different owners' folders never compare equal.
func SameSlackScopePath(a, b string) bool {
	// Two spellings of one MIGRATED crew (the old per-user path and Crew/<folder>) are the same destination
	// (PLAT-442 step 4); the fold leaves every other path as it is.
	if fa, fb := foldCrewScope(a), foldCrewScope(b); fa != a || fb != b {
		if fa == fb && workspaceref.MustParse(fa).IsShared() {
			return true
		}
		a, b = fa, fb
	}
	ra, rb := workspaceref.MustParse(a), workspaceref.MustParse(b)
	if ra.HasOwner() == rb.HasOwner() {
		return ra.SameAs(rb)
	}
	return ra.Logical() != "" && ra.Logical() == rb.Logical()
}

// NormalizeSlackChannelID canonicalizes a channel ID used as a route key.
func NormalizeSlackChannelID(channelID string) string {
	return strings.ToUpper(strings.TrimSpace(channelID))
}

// maskedSlackConnection returns a copy with credential values replaced by
// display-only placeholders. Masked values round-trip back through save
// paths as "no change".
func (c SlackConnection) masked() SlackConnection {
	out := c
	if len(c.ChannelRoutes) > 0 {
		out.ChannelRoutes = make(map[string]SlackConnectionRoute, len(c.ChannelRoutes))
		for channel, route := range c.ChannelRoutes {
			route.Targets = append([]SlackTargetRef(nil), route.Targets...)
			out.ChannelRoutes[channel] = route
		}
	}
	out.Targets = append([]SlackTargetRef(nil), c.Targets...)
	out.BotToken = maskSlackToken("xoxb-", c.BotToken)
	out.AppToken = maskSlackToken("xapp-", c.AppToken)
	return out
}

func maskSlackToken(prefix, token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return prefix + "..."
	}
	return prefix + "..." + token[len(token)-4:]
}

// complete reports whether the connection holds both tokens it needs to run.
func (c SlackConnection) complete() bool {
	return strings.TrimSpace(c.BotToken) != "" && strings.TrimSpace(c.AppToken) != ""
}

// mintSlackConnectionID allocates a registry-unique connection ID.
func mintSlackConnectionID(taken map[string]bool) string {
	for {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			continue
		}
		id := "slack_" + hex.EncodeToString(raw[:])
		if !taken[id] {
			return id
		}
	}
}

// normalizeSlackConnections repairs registry invariants in place: no blank
// or duplicate IDs, and a DefaultConnectionID that names a registry member.
// A lone platform-managed (unscoped) connection needs no explicit choice —
// it becomes the default. A lone workflow-scoped connection never does:
// inheriting platform traffic must stay an explicit admin decision.
func normalizeSlackConnections(cfg *SlackConfig) {
	if cfg == nil {
		return
	}
	seen := make(map[string]bool, len(cfg.Connections))
	out := cfg.Connections[:0]
	for _, c := range cfg.Connections {
		c.ID = strings.TrimSpace(c.ID)
		c.DisplayName = strings.TrimSpace(c.DisplayName)
		c.WorkspacePath = strings.TrimSpace(c.WorkspacePath)
		c.ProfileID = strings.TrimSpace(c.ProfileID)
		if c.ID == "" || seen[c.ID] {
			continue
		}
		if c.DisplayName == "" {
			c.DisplayName = c.ID
		}
		c.ChannelRoutes = normalizeSlackConnectionRoutes(c)
		c.Targets = normalizeSlackConnectionTargets(c)
		seen[c.ID] = true
		out = append(out, c)
	}
	cfg.Connections = out
	if cfg.DefaultConnectionID != "" && !seen[strings.TrimSpace(cfg.DefaultConnectionID)] {
		cfg.DefaultConnectionID = ""
	}
	cfg.DefaultConnectionID = strings.TrimSpace(cfg.DefaultConnectionID)
	if cfg.DefaultConnectionID == "" && len(cfg.Connections) == 1 && cfg.Connections[0].WorkspacePath == "" {
		cfg.DefaultConnectionID = cfg.Connections[0].ID
	}
}

// normalizeSlackConnectionRoutes keeps only well-formed routes on a scoped
// connection: canonical channel keys, at least one target and no Codes (they
// answer DMs only). A route naming only the bot's own target is kept: a bot
// answers only in channels listed here (PLAT-668).
func normalizeSlackConnectionRoutes(c SlackConnection) map[string]SlackConnectionRoute {
	if c.WorkspacePath == "" || len(c.ChannelRoutes) == 0 {
		return nil
	}
	out := make(map[string]SlackConnectionRoute, len(c.ChannelRoutes))
	for channel, route := range c.ChannelRoutes {
		channel = NormalizeSlackChannelID(channel)
		route.WorkspacePath = strings.TrimSpace(route.WorkspacePath)
		route.ProfileID = strings.TrimSpace(route.ProfileID)
		route.AddedBy = strings.TrimSpace(route.AddedBy)
		if route.Default().IsCode() {
			route.WorkspacePath, route.ProfileID = "", ""
		}
		var extra []SlackTargetRef
		for _, target := range route.Targets {
			target.WorkspacePath = strings.TrimSpace(target.WorkspacePath)
			target.ProfileID = strings.TrimSpace(target.ProfileID)
			target.AddedBy = strings.TrimSpace(target.AddedBy)
			if target.Empty() || target.IsCode() || target.Same(route.Default()) {
				continue
			}
			extra = append(extra, target)
		}
		route.Targets = extra
		if channel == "" || (route.WorkspacePath == "" && len(route.Targets) == 0) {
			continue
		}
		if route.Default().Empty() {
			// A trigger runs the default target; without one it has none.
			route.Trigger = nil
		}
		out[channel] = route
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeSlackConnectionTargets keeps a scoped connection's attached
// targets well-formed and distinct, without its own destination.
func normalizeSlackConnectionTargets(c SlackConnection) []SlackTargetRef {
	if c.WorkspacePath == "" || len(c.Targets) == 0 {
		return nil
	}
	own := SlackTargetRef{WorkspacePath: c.WorkspacePath, ProfileID: c.ProfileID}
	var out []SlackTargetRef
	for _, target := range c.Targets {
		target.WorkspacePath = strings.TrimSpace(target.WorkspacePath)
		target.ProfileID = strings.TrimSpace(target.ProfileID)
		target.AddedBy = strings.TrimSpace(target.AddedBy)
		if target.Empty() || target.Same(own) {
			continue
		}
		duplicate := false
		for _, seen := range out {
			if seen.Same(target) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, target)
		}
	}
	return out
}

// migrateLegacySlackConfig moves pre-registry credentials (top-level
// bot_token/app_token/enabled) into a platform-managed default connection.
// It reports whether the config changed and needs persisting.
func migrateLegacySlackConfig(cfg *SlackConfig) bool {
	if cfg == nil || len(cfg.Connections) > 0 {
		return false
	}
	botToken := strings.TrimSpace(cfg.BotToken)
	appToken := strings.TrimSpace(cfg.AppToken)
	if botToken == "" && appToken == "" {
		return false
	}
	cfg.Connections = []SlackConnection{{
		ID:          "slack_001",
		DisplayName: "Default",
		BotToken:    cfg.BotToken,
		AppToken:    cfg.AppToken,
		Enabled:     cfg.Enabled,
	}}
	cfg.DefaultConnectionID = "slack_001"
	cfg.BotToken = ""
	cfg.AppToken = ""
	cfg.Enabled = false
	return true
}

// findSlackConnection returns the registry entry with the given ID.
func findSlackConnection(cfg *SlackConfig, id string) (SlackConnection, bool) {
	if cfg == nil {
		return SlackConnection{}, false
	}
	id = strings.TrimSpace(id)
	for _, c := range cfg.Connections {
		if c.ID == id {
			return c, true
		}
	}
	return SlackConnection{}, false
}

// SlackConnectionExists reports whether id names a registry entry. It loads
// the registry from disk without starting any listener, so manifest and
// tool validation can fail fast on unknown selections.
func SlackConnectionExists(connID string) (bool, error) {
	connID = strings.TrimSpace(connID)
	if connID == "" {
		return false, nil
	}
	slackFSMu.Lock()
	defer slackFSMu.Unlock()
	cfg, err := loadSlackConfigFromDisk()
	if err != nil {
		return false, err
	}
	_, ok := findSlackConnection(cfg, connID)
	return ok, nil
}

// defaultSlackConnectionID resolves which connection an empty selection
// means: the explicit default, or the only connection when exactly one
// platform-managed connection exists. Empty means no connection is
// selected — default traffic then fails loudly instead of borrowing a
// workflow's app.
func defaultSlackConnectionID(cfg *SlackConfig) string {
	if cfg == nil {
		return ""
	}
	if id := strings.TrimSpace(cfg.DefaultConnectionID); id != "" {
		if _, ok := findSlackConnection(cfg, id); ok {
			return id
		}
	}
	if len(cfg.Connections) == 1 && cfg.Connections[0].WorkspacePath == "" {
		return cfg.Connections[0].ID
	}
	return ""
}

// effectiveSlackConnection resolves a connection selection to credentials.
// An empty selection means the default connection. When the registry is
// empty it falls back to the legacy top-level fields so directly-constructed
// configs (tests, test-before-save) keep working.
func effectiveSlackConnection(cfg *SlackConfig, id string) (SlackConnection, bool) {
	if cfg == nil {
		return SlackConnection{}, false
	}
	if len(cfg.Connections) == 0 {
		legacy := SlackConnection{BotToken: cfg.BotToken, AppToken: cfg.AppToken, Enabled: cfg.Enabled}
		if !legacy.complete() {
			return SlackConnection{}, false
		}
		return legacy, true
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = defaultSlackConnectionID(cfg)
	}
	conn, ok := findSlackConnection(cfg, id)
	if !ok || !conn.complete() {
		return SlackConnection{}, false
	}
	return conn, true
}

// validateSlackConnectionTokens rejects tokens of the wrong type. Empty
// means "not set" and is allowed here; completeness is enforced where the
// connection is used.
func validateSlackConnectionTokens(botToken, appToken string) error {
	if botToken = strings.TrimSpace(botToken); botToken != "" && !strings.HasPrefix(botToken, "xoxb-") {
		return fmt.Errorf("bot_token has the wrong token type")
	}
	if appToken = strings.TrimSpace(appToken); appToken != "" && !strings.HasPrefix(appToken, "xapp-") {
		return fmt.Errorf("app_token has the wrong token type")
	}
	return nil
}

// encryptSlackConnection encodes a connection's credentials for storage.
func encryptSlackConnection(conn SlackConnection) (SlackConnection, error) {
	var err error
	if conn.BotToken, err = encodeSlackCredential(conn.BotToken); err != nil {
		return SlackConnection{}, err
	}
	if conn.AppToken, err = encodeSlackCredential(conn.AppToken); err != nil {
		return SlackConnection{}, err
	}
	return conn, nil
}

// decryptSlackConnection decodes a connection's stored credentials.
func decryptSlackConnection(conn SlackConnection) (SlackConnection, error) {
	var err error
	if conn.BotToken, err = decodeSlackCredential(conn.BotToken); err != nil {
		return SlackConnection{}, err
	}
	if conn.AppToken, err = decodeSlackCredential(conn.AppToken); err != nil {
		return SlackConnection{}, err
	}
	return conn, nil
}
