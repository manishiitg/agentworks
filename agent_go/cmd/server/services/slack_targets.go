package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slack-go/slack"
)

// Slack slugs (docs/design/slack_slugs.md, PLAT-668): one Slack app serves
// many workflows, Crews and Codes. A channel reaches a list of allowed targets
// with an optional default; a message picks one with "@bot <slug> ...", the
// thread is bound to it, and a DM reaches the targets the matched person can
// reach. A slug is a name, never a grant: channel access comes from the
// channel's allowed list, DM access is the person's own access, and both are
// re-resolved on every message, turn and tool call.

// SlackTargetRef names one Slack target: a workflow folder, or a Crew or Code
// project (ProfileID set). It names a destination only; the server resolves
// the runnable route from it every time.
type SlackTargetRef struct {
	WorkspacePath string `json:"workspace_path"`
	ProfileID     string `json:"profile_id,omitempty"`
	// AddedBy records who attached the target, for audit.
	AddedBy string `json:"added_by,omitempty"`
}

// Same reports whether two refs name the same target.
func (r SlackTargetRef) Same(other SlackTargetRef) bool {
	return strings.TrimSpace(r.WorkspacePath) != "" &&
		SameSlackScopePath(r.WorkspacePath, other.WorkspacePath) &&
		strings.EqualFold(strings.TrimSpace(r.ProfileID), strings.TrimSpace(other.ProfileID))
}

// Empty reports a ref that names nothing.
func (r SlackTargetRef) Empty() bool { return strings.TrimSpace(r.WorkspacePath) == "" }

// Key is a stable string form, used as a button value.
func (r SlackTargetRef) Key() string {
	return strings.TrimSpace(r.ProfileID) + "|" + strings.TrimSpace(r.WorkspacePath)
}

// IsCode reports a Code project target. Codes answer 1:1 DMs only.
func (r SlackTargetRef) IsCode() bool {
	return strings.EqualFold(strings.TrimSpace(r.ProfileID), "code")
}

// SlackTargetRefFromRoute names a route's destination.
func SlackTargetRefFromRoute(route ChannelRoute) SlackTargetRef {
	return SlackTargetRef{WorkspacePath: strings.TrimSpace(route.WorkspacePath), ProfileID: strings.TrimSpace(route.ProfileID)}
}

// SlackTargetProduct is the product a target belongs to, for the admin's
// "which products may use the platform bot" limit.
func SlackTargetProduct(ref SlackTargetRef) string {
	switch profile := strings.ToLower(strings.TrimSpace(ref.ProfileID)); profile {
	case "":
		return "workflows"
	case "work":
		return "crew"
	default:
		return profile
	}
}

// SlackTarget is one reachable target with its slug and display label.
type SlackTarget struct {
	Ref   SlackTargetRef
	Slug  string
	Label string
	// Legacy marks the shared bot's admin channel route (bot-connectors.json
	// allowed_channels); its route is the stored one, with its trigger and
	// email exclusions.
	Legacy bool
}

// SlackTargetSet is what one app reaches in one place: a channel's allowed
// list, or the candidates for a DM.
type SlackTargetSet struct {
	Targets []SlackTarget
	// Default indexes the target that answers when nothing is picked; -1 none.
	// For a DM it is the app's own target, when it has one.
	Default int
	// Dedicated: the app is a workflow's, Crew's or Code's own bot.
	Dedicated bool
	// Disabled: an own bot that is switched off or no longer resolves; its
	// threads are refused, never handed to another destination.
	Disabled bool
	// Platform: the platform (shared) bot.
	Platform bool
}

// Find returns the index of ref in the set, or -1.
func (s SlackTargetSet) Find(ref SlackTargetRef) int {
	for i, target := range s.Targets {
		if target.Ref.Same(ref) {
			return i
		}
	}
	return -1
}

// WithSlug returns the indexes of the targets named slug.
func (s SlackTargetSet) WithSlug(slug string) []int {
	slug = strings.ToLower(strings.TrimSpace(slug))
	var out []int
	if slug == "" {
		return nil
	}
	for i, target := range s.Targets {
		if target.Slug == slug {
			out = append(out, i)
		}
	}
	return out
}

// Slugs lists the distinct slugs in the set, sorted.
func (s SlackTargetSet) Slugs() []string {
	seen := map[string]bool{}
	var out []string
	for _, target := range s.Targets {
		if target.Slug != "" && !seen[target.Slug] {
			seen[target.Slug] = true
			out = append(out, target.Slug)
		}
	}
	sort.Strings(out)
	return out
}

// SlackRoutingHooks are installed by the server, which owns manifests,
// ownership and access. Without them Slack keeps the one-route-per-channel
// rule (DedicatedSlackRoute and the shared bot's channel routes).
type SlackRoutingHooks struct {
	// Channel lists the targets allowed in a channel on an app.
	Channel func(ctx context.Context, connectionID, channelID string) SlackTargetSet
	// DM lists the targets an app offers in DMs, before the person's own
	// access is checked.
	DM func(ctx context.Context, connectionID string) SlackTargetSet
	// Route resolves a target to its runnable route. channelID is "" for a
	// DM. An error means the target is unavailable (gone, or no longer
	// opted in).
	Route func(ctx context.Context, connectionID, channelID string, target SlackTarget) (*ChannelRoute, error)
	// CanReach reports whether an AgentWorks account can reach a route in a
	// DM with its own access (owner, reader, shared-with; Code owner only).
	CanReach func(ctx context.Context, accountID string, route ChannelRoute) bool
	// Trigger is the route whose saved trigger applies to top-level messages
	// in a channel on an app (an own bot's channel route, or the shared
	// bot's admin route), or nil. Optional.
	Trigger func(ctx context.Context, connectionID, channelID string) *ChannelRoute
}

type slackTriggerConnectionKey struct{}

// WithSlackTriggerConnection records the app a trigger event arrived on.
func WithSlackTriggerConnection(ctx context.Context, connectionID string) context.Context {
	return context.WithValue(ctx, slackTriggerConnectionKey{}, strings.TrimSpace(connectionID))
}

// SlackTriggerConnection is the app a trigger event arrived on ("" = the
// default connection).
func SlackTriggerConnection(ctx context.Context) string {
	id, _ := ctx.Value(slackTriggerConnectionKey{}).(string)
	return id
}

var slackRoutingHooks atomic.Pointer[SlackRoutingHooks]

// SetSlackRoutingHooks installs the server's slug routing (nil removes it).
func SetSlackRoutingHooks(hooks *SlackRoutingHooks) {
	slackRoutingHooks.Store(hooks)
}

func currentSlackRoutingHooks() *SlackRoutingHooks {
	hooks := slackRoutingHooks.Load()
	if hooks == nil || hooks.Channel == nil || hooks.DM == nil || hooks.Route == nil || hooks.CanReach == nil {
		return nil
	}
	return hooks
}

// SlackRoutingInstalled reports whether slug routing is wired.
func SlackRoutingInstalled() bool { return currentSlackRoutingHooks() != nil }

// IsSlackDMChannel reports a 1:1 DM conversation ID.
func IsSlackDMChannel(channelID string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(channelID)), "D")
}

// --- slugs ---

// SlackSlug turns a target name into a slug: lowercase [a-z0-9-], the
// WhatsApp rule.
func SlackSlug(name string) string {
	slug := slugifyWhatsAppWorkflow(name)
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}

// slackReservedWords are bot commands, never slugs.
var slackReservedWords = map[string]bool{"list": true, "help": true, "status": true, "resume": true}

// ValidSlackSlug reports a slug an owner may save.
func ValidSlackSlug(slug string) error {
	if slug == "" || len(slug) > 40 || !isValidSlug(slug) || strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return fmt.Errorf("a slug is 1-40 lowercase letters, digits and dashes")
	}
	if slackReservedWords[slug] {
		return fmt.Errorf("%q is a bot command and cannot be a slug", slug)
	}
	return nil
}

// SplitSlackSlugWord returns the first word of a message as a slug candidate
// (lowercased, an optional leading "@" and trailing ":" or "," dropped) and
// the rest of the message. The caller decides whether the word is a slug:
// only a word on the allowed list is one; anything else is message text.
func SplitSlackSlugWord(text string) (word, rest string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", ""
	}
	end := strings.IndexAny(trimmed, " \t\n")
	first, rest := trimmed, ""
	if end >= 0 {
		first, rest = trimmed[:end], strings.TrimSpace(trimmed[end:])
	}
	first = strings.TrimPrefix(first, "@")
	first = strings.TrimRight(first, ":,")
	return strings.ToLower(first), rest
}

// PickSlackSlug parses "<slug> ..." against a target set. matches are the
// targets the first word names (none when it is not an allowed slug, in which
// case rest is the whole text). A slug not on the list is message text,
// never a target.
func PickSlackSlug(text string, set SlackTargetSet) (matches []int, slug, rest string) {
	word, remainder := SplitSlackSlugWord(text)
	if word == "" {
		return nil, "", strings.TrimSpace(text)
	}
	matches = set.WithSlug(word)
	if len(matches) == 0 {
		return nil, "", strings.TrimSpace(text)
	}
	return matches, word, remainder
}

// FormatSlackTargetList is the "list" reply.
func FormatSlackTargetList(set SlackTargetSet, where string) string {
	if len(set.Targets) == 0 {
		return "Nothing is reachable " + where + " yet."
	}
	var b strings.Builder
	b.WriteString("Reachable " + where + ":\n")
	for i, target := range set.Targets {
		label := firstNonEmptyString(target.Label, target.Slug)
		line := fmt.Sprintf("- `%s` %s", target.Slug, label)
		if i == set.Default {
			line += " (default)"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\nStart a message with a slug to pick one, e.g. `" + set.Targets[0].Slug + " what changed today?`")
	return b.String()
}

// --- the platform bot's registry ---

// SlackTargetSettings is one target's Slack settings: its slug and whether
// its owner turned on "Use the AgentWorks bot".
type SlackTargetSettings struct {
	WorkspacePath string    `json:"workspace_path"`
	ProfileID     string    `json:"profile_id,omitempty"`
	Slug          string    `json:"slug,omitempty"`
	Label         string    `json:"label,omitempty"`
	OwnerID       string    `json:"owner_id,omitempty"`
	PlatformBot   bool      `json:"platform_bot"`
	UpdatedBy     string    `json:"updated_by,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// Ref names the target.
func (s SlackTargetSettings) Ref() SlackTargetRef {
	return SlackTargetRef{WorkspacePath: s.WorkspacePath, ProfileID: s.ProfileID}
}

// SlackPlatformChannel is a platform-bot channel's owner-added targets. The
// admin's channel route (allowed_channels) stays where it is and is the
// channel's default when it has one.
type SlackPlatformChannel struct {
	Targets []SlackTargetRef `json:"targets,omitempty"`
	Default *SlackTargetRef  `json:"default,omitempty"`
}

// SlackTargetsRegistry is config/slack-targets.json.
type SlackTargetsRegistry struct {
	Targets  []SlackTargetSettings           `json:"targets,omitempty"`
	Channels map[string]SlackPlatformChannel `json:"channels,omitempty"`
	// Products limits which products may use the platform bot ("workflows",
	// "crew", "code"); empty means all. Admin-managed.
	Products []string `json:"products,omitempty"`
}

// Settings returns a target's entry.
func (r *SlackTargetsRegistry) Settings(ref SlackTargetRef) (SlackTargetSettings, bool) {
	if r == nil {
		return SlackTargetSettings{}, false
	}
	for _, entry := range r.Targets {
		if entry.Ref().Same(ref) {
			return entry, true
		}
	}
	return SlackTargetSettings{}, false
}

// PutSettings adds or replaces a target's entry.
func (r *SlackTargetsRegistry) PutSettings(entry SlackTargetSettings) {
	for i, existing := range r.Targets {
		if existing.Ref().Same(entry.Ref()) {
			r.Targets[i] = entry
			return
		}
	}
	r.Targets = append(r.Targets, entry)
}

// ProductAllowed reports whether a target's product may use the platform bot.
func (r *SlackTargetsRegistry) ProductAllowed(ref SlackTargetRef) bool {
	if r == nil || len(r.Products) == 0 {
		return true
	}
	product := SlackTargetProduct(ref)
	for _, allowed := range r.Products {
		if strings.EqualFold(strings.TrimSpace(allowed), product) {
			return true
		}
	}
	return false
}

// PlatformOptIn reports whether a target may answer on the platform bot. An
// explicit entry decides. Without one, a target that already has the admin's
// channel route (legacyRoute) keeps answering: that is the migration of
// setups made before the switch existed.
func (r *SlackTargetsRegistry) PlatformOptIn(ref SlackTargetRef, legacyRoute bool) bool {
	if !r.ProductAllowed(ref) {
		return false
	}
	if entry, ok := r.Settings(ref); ok {
		return entry.PlatformBot
	}
	return legacyRoute
}

func slackTargetsFilePath() string { return "config/slack-targets.json" }

var slackTargetsMu sync.Mutex

// LoadSlackTargetsRegistry reads the registry; a missing file is empty.
func LoadSlackTargetsRegistry(ctx context.Context) (*SlackTargetsRegistry, error) {
	raw, found, err := readWorkspaceFile(ctx, workspaceAPIURL(), slackTargetsFilePath())
	if err != nil {
		return nil, err
	}
	registry := &SlackTargetsRegistry{}
	if found && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), registry); err != nil {
			return nil, fmt.Errorf("parse %s: %w", slackTargetsFilePath(), err)
		}
	}
	if registry.Channels == nil {
		registry.Channels = map[string]SlackPlatformChannel{}
	}
	return registry, nil
}

// ModifySlackTargetsRegistry loads, mutates and saves the registry under one
// lock. Authorization is the caller's job.
func ModifySlackTargetsRegistry(ctx context.Context, mutate func(*SlackTargetsRegistry) error) error {
	slackTargetsMu.Lock()
	defer slackTargetsMu.Unlock()
	registry, err := LoadSlackTargetsRegistry(ctx)
	if err != nil {
		return err
	}
	if err := mutate(registry); err != nil {
		return err
	}
	for channel, entry := range registry.Channels {
		if len(entry.Targets) == 0 {
			delete(registry.Channels, channel)
		}
	}
	raw, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return writeWorkspaceFile(ctx, workspaceAPIURL(), slackTargetsFilePath(), string(raw))
}

// --- thread bindings ---

// SlackThreadTarget binds a Slack thread (or a whole DM) to the target it
// picked. It is stored beside the thread's durable session binding and is
// revalidated against the channel's allowed list on every message.
type SlackThreadTarget struct {
	Ref     SlackTargetRef `json:"ref"`
	Slug    string         `json:"slug,omitempty"`
	BoundBy string         `json:"bound_by,omitempty"`
	BoundAt time.Time      `json:"bound_at"`
}

// slackTargetBindingThread is the thread a binding is stored under: a DM is
// one conversation, so its choice is remembered for the whole DM.
func slackTargetBindingThread(thread ThreadID) ThreadID {
	thread.Platform = "slack"
	if IsSlackDMChannel(thread.ChannelID) {
		thread.ThreadTS = thread.ChannelID
	}
	return thread
}

func slackThreadTargetPath(thread ThreadID) string {
	sum := sha256.Sum256([]byte(slackTargetBindingThread(thread).Key()))
	return fmt.Sprintf("config/slack-threads/%x.target.json", sum)
}

// LoadSlackThreadTarget reads a thread's (or DM's) bound target.
func LoadSlackThreadTarget(ctx context.Context, thread ThreadID) (SlackThreadTarget, bool) {
	if strings.TrimSpace(thread.ChannelID) == "" || strings.TrimSpace(thread.ThreadTS) == "" && !IsSlackDMChannel(thread.ChannelID) {
		return SlackThreadTarget{}, false
	}
	raw, found, err := readWorkspaceFile(ctx, workspaceAPIURL(), slackThreadTargetPath(thread))
	if err != nil || !found || strings.TrimSpace(raw) == "" {
		return SlackThreadTarget{}, false
	}
	var binding SlackThreadTarget
	if json.Unmarshal([]byte(raw), &binding) != nil || binding.Ref.Empty() {
		return SlackThreadTarget{}, false
	}
	return binding, true
}

// SaveSlackThreadTarget binds a thread (or DM) to a target.
func SaveSlackThreadTarget(ctx context.Context, thread ThreadID, binding SlackThreadTarget) error {
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now().UTC()
	}
	binding.Ref.AddedBy = ""
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	return writeWorkspaceFile(ctx, workspaceAPIURL(), slackThreadTargetPath(thread), string(raw))
}

// --- route resolution ---

// slackSetRoute resolves one target of a set, or the revoked sentinel when it
// no longer resolves.
func slackSetRoute(ctx context.Context, hooks *SlackRoutingHooks, connectionID, channelID string, target SlackTarget) *ChannelRoute {
	route, err := hooks.Route(ctx, connectionID, channelID, target)
	if err != nil || route == nil {
		return RevokedSlackRoute()
	}
	return route
}

// ResolveSlackThreadRoute is the route a message, turn or follow-up in a
// Slack thread runs on: the thread's bound target while it is still allowed
// there, else the channel's (or the DM's own) default. A bound target that
// is no longer allowed, or an own bot that is off, is revoked, never handed
// to another destination. legacy is the pre-slug rule, used when slug
// routing is not installed.
func ResolveSlackThreadRoute(ctx context.Context, thread ThreadID, legacy func() *ChannelRoute) *ChannelRoute {
	hooks := currentSlackRoutingHooks()
	if hooks == nil {
		return ResolveSlackRoute(ctx, thread.ConnectionID, thread.ChannelID, legacy)
	}
	channelID := NormalizeSlackChannelID(thread.ChannelID)
	var set SlackTargetSet
	routeChannel := channelID
	if IsSlackDMChannel(channelID) {
		set = hooks.DM(ctx, thread.ConnectionID)
		routeChannel = ""
	} else {
		set = hooks.Channel(ctx, thread.ConnectionID, channelID)
	}
	if set.Disabled {
		return RevokedSlackRoute()
	}
	if bound, found := LoadSlackThreadTarget(ctx, thread); found {
		index := set.Find(bound.Ref)
		if index < 0 {
			return RevokedSlackRoute()
		}
		return slackSetRoute(ctx, hooks, thread.ConnectionID, routeChannel, set.Targets[index])
	}
	if set.Default >= 0 && set.Default < len(set.Targets) {
		return slackSetRoute(ctx, hooks, thread.ConnectionID, routeChannel, set.Targets[set.Default])
	}
	return nil
}

// --- the button prompt ---

// slackPendingPick is a message waiting for its sender to pick a target.
type slackPendingPick struct {
	msg     BotIncomingMessage
	raw     *slack.Msg // its files, downloaded once a target is picked
	choices []SlackTarget
	at      time.Time
}

const slackPendingPickTTL = 30 * time.Minute

var (
	slackPendingPicksMu sync.Mutex
	slackPendingPicks   = map[string]slackPendingPick{}
)

func rememberSlackPendingPick(thread ThreadID, msg BotIncomingMessage, raw *slack.Msg, choices []SlackTarget) {
	slackPendingPicksMu.Lock()
	defer slackPendingPicksMu.Unlock()
	cutoff := time.Now().Add(-slackPendingPickTTL)
	for key, pending := range slackPendingPicks {
		if pending.at.Before(cutoff) {
			delete(slackPendingPicks, key)
		}
	}
	slackPendingPicks[slackTargetBindingThread(thread).Key()] = slackPendingPick{msg: msg, raw: raw, choices: choices, at: time.Now()}
}

func takeSlackPendingPick(thread ThreadID) (slackPendingPick, bool) {
	slackPendingPicksMu.Lock()
	defer slackPendingPicksMu.Unlock()
	key := slackTargetBindingThread(thread).Key()
	pending, ok := slackPendingPicks[key]
	if ok {
		delete(slackPendingPicks, key)
	}
	if !ok || time.Since(pending.at) > slackPendingPickTTL {
		return slackPendingPick{}, false
	}
	return pending, true
}

// slackPickActionPrefix starts the action ID of a target button.
const slackPickActionPrefix = "slack_pick_"

// slackPickBlocks builds one button per target (Slack allows 25 per block).
func slackPickBlocks(choices []SlackTarget) []MessageBlock {
	var blocks []MessageBlock
	var buttons []MessageButton
	for i, choice := range choices {
		if i >= 50 {
			break
		}
		label := firstNonEmptyString(choice.Label, choice.Slug)
		if len(label) > 70 {
			label = label[:70]
		}
		buttons = append(buttons, MessageButton{Text: label, Value: choice.Ref.Key(), ActionID: fmt.Sprintf("%s%d", slackPickActionPrefix, i)})
		if len(buttons) == 25 {
			blocks = append(blocks, MessageBlock{Type: "actions", Buttons: buttons})
			buttons = nil
		}
	}
	if len(buttons) > 0 {
		blocks = append(blocks, MessageBlock{Type: "actions", Buttons: buttons})
	}
	return blocks
}

// slackRefFromKey parses SlackTargetRef.Key.
func slackRefFromKey(key string) SlackTargetRef {
	profile, path, _ := strings.Cut(strings.TrimSpace(key), "|")
	return SlackTargetRef{WorkspacePath: strings.TrimSpace(path), ProfileID: strings.TrimSpace(profile)}
}

// slackPickPrompt is the question above the buttons.
func slackPickPrompt(choices []SlackTarget, slug string) string {
	if slug != "" {
		return fmt.Sprintf("Several targets are called `%s` here. Which one did you mean? Your message runs once you pick, and this thread stays with it.", slug)
	}
	names := make([]string, 0, len(choices))
	for _, choice := range choices {
		names = append(names, "`"+choice.Slug+"`")
	}
	return "Who should answer? Pick one, and this thread stays with it. Next time you can start with a slug: " + strings.Join(names, ", ") + "."
}
