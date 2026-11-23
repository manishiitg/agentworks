package llmtypes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

// WithModel sets the model ID
func WithModel(model string) CallOption {
	return func(opts *CallOptions) {
		opts.Model = model
	}
}

// WithTemperature sets the temperature
func WithTemperature(temperature float64) CallOption {
	return func(opts *CallOptions) {
		opts.Temperature = temperature
	}
}

// WithMaxTokens sets the maximum tokens
func WithMaxTokens(maxTokens int) CallOption {
	return func(opts *CallOptions) {
		opts.MaxTokens = maxTokens
	}
}

// WithTopP sets the nucleus-sampling probability cutoff. Pass a value in
// (0, 1]; 1.0 effectively disables nucleus sampling. Zero is treated as
// "do not set" and the provider's own default applies.
func WithTopP(topP float64) CallOption {
	return func(opts *CallOptions) {
		opts.TopP = topP
	}
}

// WithTopK sets the top-k sampling cutoff. Only forwarded to providers
// that accept top_k (Anthropic Messages API does; OpenAI Chat Completions
// does not). Pass zero to leave the provider's default in place.
func WithTopK(topK int) CallOption {
	return func(opts *CallOptions) {
		opts.TopK = topK
	}
}

// WithInspectorSink attaches a debug-event sink for this call. Adapters
// that participate in the inspector contract will emit
// InspectorEvents at request/event/tool_call/completion/error
// boundaries. Pass nil (the default) to disable inspector emission
// entirely.
func WithInspectorSink(sink InspectorSink) CallOption {
	return func(opts *CallOptions) {
		opts.InspectorSink = sink
	}
}

// WithStopSequences sets the strings that, if generated, terminate
// sampling immediately. Pass an empty slice (or nil) to clear.
func WithStopSequences(seqs []string) CallOption {
	return func(opts *CallOptions) {
		if seqs == nil {
			opts.StopSequences = nil
			return
		}
		out := make([]string, 0, len(seqs))
		for _, s := range seqs {
			if s != "" {
				out = append(out, s)
			}
		}
		opts.StopSequences = out
	}
}

// WithJSONMode enables JSON mode
func WithJSONMode() CallOption {
	return func(opts *CallOptions) {
		opts.JSONMode = true
	}
}

// WithJSONSchema enables JSON Schema structured outputs
// schema: The JSON Schema definition as a map
// name: The name of the schema
// description: Description of what the schema represents
// strict: Whether to enforce strict schema compliance (default: true)
func WithJSONSchema(schema map[string]interface{}, name, description string, strict bool) CallOption {
	return func(opts *CallOptions) {
		opts.JSONSchema = &JSONSchemaConfig{
			Name:        name,
			Description: description,
			Schema:      schema,
			Strict:      strict,
		}
	}
}

// WithTools sets the tools available for the LLM
func WithTools(tools []Tool) CallOption {
	return func(opts *CallOptions) {
		opts.Tools = tools
	}
}

// WithToolChoice sets the tool choice strategy
func WithToolChoice(toolChoice *ToolChoice) CallOption {
	return func(opts *CallOptions) {
		opts.ToolChoice = toolChoice
	}
}

// WithToolChoiceString creates a ToolChoice from a string type ("auto", "none", "required") and sets it
func WithToolChoiceString(choiceType string) CallOption {
	return func(opts *CallOptions) {
		opts.ToolChoice = &ToolChoice{Type: choiceType}
	}
}

// WithStreamingChan sets the streaming channel for receiving chunks
// The channel receives structured StreamChunk objects that can be either content or tool calls
// The channel will be closed when streaming completes
func WithStreamingChan(ch chan<- StreamChunk) CallOption {
	return func(opts *CallOptions) {
		opts.StreamChan = ch
	}
}

// WithCodingProviderLaunchOnly asks tmux-backed coding-agent adapters to start
// or reacquire their interactive TUI and return once the prompt is ready,
// without sending a user message.
func WithCodingProviderLaunchOnly() CallOption {
	return func(opts *CallOptions) {
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{Custom: make(map[string]interface{})}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = make(map[string]interface{})
		}
		opts.Metadata.Custom[CodingProviderLaunchOnlyMetadataKey] = true
	}
}

func CodingProviderLaunchOnlyFromOptions(opts *CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, _ := opts.Metadata.Custom[CodingProviderLaunchOnlyMetadataKey].(bool)
	return enabled
}

// WithCodingProviderLaunchSystemPrompt carries the agent's accumulated
// system prompt through the launch-only contract so the adapter can
// project its provider-specific rule file (.cursor/rules/mlp-system.mdc,
// .agents/rules/mlp-system.md, AGENTS.md, GEMINI.md, CLAUDE.md, etc.)
// even though no user message is being sent. Without this, launch-only
// (used by the resumed-terminal restore path) hits the adapter with
// nil messages → split*SystemPrompt returns empty → the rule file is
// never written for that session.
func WithCodingProviderLaunchSystemPrompt(systemPrompt string) CallOption {
	return func(opts *CallOptions) {
		if strings.TrimSpace(systemPrompt) == "" {
			return
		}
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{Custom: make(map[string]interface{})}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = make(map[string]interface{})
		}
		opts.Metadata.Custom[CodingProviderLaunchSystemPromptMetadataKey] = systemPrompt
	}
}

// CodingProviderLaunchSystemPromptFromOptions returns the launch-only
// system prompt (if any) injected via WithCodingProviderLaunchSystemPrompt.
// Empty string when not set.
func CodingProviderLaunchSystemPromptFromOptions(opts *CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	prompt, _ := opts.Metadata.Custom[CodingProviderLaunchSystemPromptMetadataKey].(string)
	return prompt
}

// WithAttachedSkills threads the agent's attached skills through the
// call so CLI adapters can project them to the provider's working
// directory at session launch. mcpagent populates this from
// Agent.attachedSkills before every LLM call; CLI adapters read it via
// AttachedSkillsFromOptions(opts) and call their own ProjectSkills
// method. API adapters don't need to read it — the listing is already
// in the system prompt by the time the request goes out.
func WithAttachedSkills(skills []*Skill) CallOption {
	return func(opts *CallOptions) {
		if len(skills) == 0 {
			return
		}
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{Custom: make(map[string]interface{})}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = make(map[string]interface{})
		}
		opts.Metadata.Custom[AttachedSkillsMetadataKey] = skills
	}
}

// AttachedSkillsFromOptions returns the skills threaded by
// WithAttachedSkills, or nil when none are attached. Safe to call on
// nil or partially-initialized CallOptions.
func AttachedSkillsFromOptions(opts *CallOptions) []*Skill {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return nil
	}
	skills, _ := opts.Metadata.Custom[AttachedSkillsMetadataKey].([]*Skill)
	return skills
}

// WithCodingAgentSecretEnvironment passes scoped secrets, workflow variables,
// and the session-bound MCP API routes to a native coding-agent process for
// this call. Values stay out of prompts, logs, and persisted call metadata.
// The MCP routes let a product use native Bash for documented product APIs
// without exposing AgentWorks' execute_shell_command bridge.
//
// Admission is decided by IsScopedCodingAgentEnvironmentKey, which is the only
// place that policy lives.
func WithCodingAgentSecretEnvironment(environment map[string]string) CallOption {
	copy := map[string]string{}
	for key, value := range environment {
		key = strings.TrimSpace(key)
		if IsScopedCodingAgentEnvironmentKey(key) && value != "" {
			copy[key] = value
		}
	}
	return func(opts *CallOptions) {
		// Deliberately records the scope even when it is EMPTY. Returning early
		// here made "this child gets zero scoped credentials" impossible to
		// express: an empty map looked identical to never calling the option,
		// so both fell through to legacy passthrough and the child kept every
		// ambient credential -- the exact opposite of what the caller asked
		// for. Presence of the key means "a scope was declared"; its contents
		// are what was granted.
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{Custom: make(map[string]interface{})}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = make(map[string]interface{})
		}
		opts.Metadata.Custom[CodingAgentSecretEnvironmentMetadataKey] = copy
	}
}

// IsScopedCodingAgentEnvironmentKey reports whether a key may be passed into a
// native coding-agent process. It is the single owner of that policy: the
// option setter, the reader, and the subprocess merge all consult it, and
// mcpagent calls it rather than keeping its own copy.
//
// One copy matters because the drift is silent. The MCP_CUSTOM_set=no failure
// was exactly this: AgentWorks created the routes, a second list one layer down
// admitted only SECRET_*, and the provider then faithfully passed on an
// already-filtered environment. Nothing errored — the child simply did not see
// the key, which is indistinguishable from the feature not existing.
//
// Selected values, session routes and the backend-built workflow runtime
// environment are admitted:
//
//	SECRET_*  selected secret values
//	VAR_*     workflow variables, VAR_WORKSPACE_PATH, VAR_GROUP_NAME
//	MCP_*     the session-bound API routes, closed set below
//	CLAUDE_CODE_DISABLE_AUTO_MEMORY  disables Claude's private provider memory
//	workflow paths and access flags use the same child scope as the bridge
//
// VAR_* is not optional. mcpagent's prompt builder documents $VAR_<NAME> to
// every coding agent as the way to read workflow config and tells it to fail
// loudly when one is missing, so dropping the prefix produces an agent doing
// exactly as instructed: failing, and blaming the workflow. It would also fail
// only on the native-shell transport while the bridge transport kept working,
// which presents as a transport-dependent workflow bug.
//
// To admit a new key: add it here, and add a case to the cross-module contract
// test. Both, or the next reader inherits the same silent failure.
func IsScopedCodingAgentEnvironmentKey(key string) bool {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "SECRET_") || strings.HasPrefix(key, "VAR_") {
		return true
	}
	if key == "CLAUDE_CODE_DISABLE_AUTO_MEMORY" {
		return true
	}
	return isScopedCodingAgentMCPEnvironmentKey(key) || isScopedWorkflowEnvironmentKey(key)
}

// These names are built by the workflow runtime. Admission here does not
// change a regular chat's inherited environment.
func isScopedWorkflowEnvironmentKey(key string) bool {
	if strings.HasPrefix(key, "WORKFLOW_FOLDER_") || strings.HasPrefix(key, "WORKFLOW_KB_") || strings.HasPrefix(key, "WORKFLOW_CREW_") {
		return true
	}
	switch key {
	case "STEP_OUTPUT_DIR", "STEP_EXECUTION_DIR", "SCRIPT_SOURCE_DIR", "DB_PATH", "RUN_FOLDER",
		"WORKFLOW_CODE_ROOT", "WORKFLOW_CODE_DEPS", "PYTHONPATH",
		"WORKFLOW_TRIGGER_INPUT_FILE", "WORKFLOW_TRIGGER_CONTEXT_FILE", "WORKFLOW_DB_ACCESS":
		return true
	default:
		return false
	}
}

// isScopedCredentialEnvironmentKey is the subset of the scoped namespace that
// carries per-child credentials and configuration, as opposed to the MCP
// transport routes. Only this subset is scrubbed when a scoped environment is
// declared -- see MergeCodingAgentSecretEnvironment for why the routes are
// deliberately excluded.
// isServerOwnedSecretKey names what the host process holds for its own use: the signing and encryption
// secret, the app's login password and legacy user list, the server and bridge tokens, and the
// deployment's global secrets. A coding agent launched from the host never inherits these, whether or not
// a secret scope was declared for the call.
// PlatformSecretEnvNames and PlatformSecretEnvPrefixes are the server's own secrets and settings that no process started
// as a person's slot may hold (the Claude SDK bridge held GOG_KEYRING_PASSWORD, SUPABASE_*, ADMIN_USERS, AUTH_*,
// PLAT-872-86). The same list as the workspace launcher's platformSecretEnvNames; a workspace test keeps them equal.
var (
	PlatformSecretEnvNames = []string{"AUTH_SECRET", "GOG_KEYRING_PASSWORD", "ADMIN_USERS", "CAPLAYER_SERVICE_TOKEN_FILE",
		"WORKSPACE_API_TOKEN", "MCP_SERVER_API_TOKEN", "MCP_BRIDGE_TOKEN_SECRET", "ACCESS_PASSWORD", "AUTH_USERS", "SSH_AUTH_SOCK"}
	PlatformSecretEnvPrefixes = []string{"VAULT_", "GLOBAL_SECRET_", "CAPLAYER_SERVICE_", "DATABASE_URL", "JWT_SECRET",
		"LANGFUSE_", "SUPABASE_", "AUTH_", "GATEWAY_"}
)

// IsPlatformSecretEnvName reports a name in PlatformSecretEnvNames/Prefixes or in AGENTWORKS_CLI_ENV_DENY.
func IsPlatformSecretEnvName(name string) bool {
	for _, n := range PlatformSecretEnvNames {
		if n == name {
			return true
		}
	}
	for _, extra := range strings.Split(os.Getenv("AGENTWORKS_CLI_ENV_DENY"), ",") {
		if name != "" && strings.TrimSpace(extra) == name {
			return true
		}
	}
	for _, prefix := range PlatformSecretEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// IsServerOwnedSecretKey reports a variable the host holds for its own use (see isServerOwnedSecretKey).
func IsServerOwnedSecretKey(key string) bool { return isServerOwnedSecretKey(key) }

func isServerOwnedSecretKey(key string) bool {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "GLOBAL_SECRET_") {
		return true
	}
	switch key {
	case "AUTH_SECRET", "ACCESS_PASSWORD", "AUTH_USERS", "MCP_SERVER_API_TOKEN", "MCP_BRIDGE_TOKEN_SECRET", "WORKSPACE_API_TOKEN":
		return true
	}
	return false
}

func isScopedCredentialEnvironmentKey(key string) bool {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "SECRET_") || strings.HasPrefix(key, "VAR_") {
		return true
	}
	// MCP_* splits in two, and treating the whole prefix as "routing metadata"
	// was wrong: MCP_API_TOKEN and MCP_AUTH are bearer credentials and
	// MCP_SESSION_ID is the session binding. Inheriting an ambient one lets a
	// child act with another session's API authority and identity, which is
	// the same class of leak as an inherited SECRET_*. Only the address-style
	// routes below are safe to inherit.
	switch key {
	case "MCP_API_TOKEN", "MCP_AUTH", "MCP_SESSION_ID":
		return true
	// The host's admin key for its auth service (Supabase): it invites people
	// by email and must never reach an agent that shares the host's environment.
	case "SUPABASE_SERVICE_ROLE_KEY":
		return true
	default:
		return false
	}
}

func isScopedCodingAgentMCPEnvironmentKey(key string) bool {
	switch key {
	case "MCP_API_URL", "MCP_API_TOKEN", "MCP_SESSION_ID", "MCP_MCP", "MCP_CUSTOM", "MCP_VIRTUAL", "MCP_AUTH":
		return true
	default:
		return false
	}
}

// CodingAgentScopeDeclared reports whether the caller supplied a scope at all,
// independent of whether that scope granted anything. This is the distinction
// between "no isolation requested" (legacy passthrough) and "isolation
// requested, granting nothing" (scrub everything).
func CodingAgentScopeDeclared(opts *CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	_, declared := opts.Metadata.Custom[CodingAgentSecretEnvironmentMetadataKey]
	return declared
}

func CodingAgentSecretEnvironmentFromOptions(opts *CallOptions) map[string]string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return nil
	}
	environment, _ := opts.Metadata.Custom[CodingAgentSecretEnvironmentMetadataKey].(map[string]string)
	copy := make(map[string]string, len(environment))
	for key, value := range environment {
		if IsScopedCodingAgentEnvironmentKey(key) && value != "" {
			copy[key] = value
		}
	}
	return copy
}

// ScopedCodingAgentEnvironmentPlan is the interactive/tmux counterpart of
// MergeCodingAgentSecretEnvironment. A structured launch can hand the child a
// fully-built environment; a tmux launch cannot, because the pane inherits the
// tmux SERVER's environment rather than this process's, so there is no slice to
// filter. The equivalent has to be expressed as "export these, unset those" and
// executed inside the launch script, where the unset applies no matter which
// layer the variable arrived from.
//
// Returns the declared entries to export, and the scoped credential keys that
// are present ambiently but were NOT declared for this child. alreadySet lists
// entries the adapter is exporting itself (its own provider API key, the MCP
// bridge routes it derived from the caller's config); those are never unset,
// or the launch would strip the credentials it just built.
//
// With nothing declared, both results are empty: a caller that never opted into
// scoping keeps exactly the behavior it had.
func ScopedCodingAgentEnvironmentPlan(ambient, alreadySet []string, opts *CallOptions) (export, unset []string) {
	export = mergeProviderAccountEnvironment(nil, opts)
	if len(ProviderAccountEnvironment(opts)) > 0 {
		unset = append(unset, ProviderAccountCredentialNames()...)
	}
	if key := codingAgentReleaseSessionKey(opts); key != "" {
		export = append(export, "AGENTWORKS_CLI_SESSION_KEY="+key)
	}
	// Server-owned secrets are removed from every launch, scope declared or not.
	serverOwned := map[string]bool{}
	for _, entry := range ambient {
		if key, _, found := strings.Cut(entry, "="); found && isServerOwnedSecretKey(key) && !serverOwned[key] {
			serverOwned[key] = true
			unset = append(unset, key)
		}
	}
	if !CodingAgentScopeDeclared(opts) {
		sort.Strings(unset)
		return export, unset
	}
	declared := CodingAgentSecretEnvironmentFromOptions(opts)

	keys := make([]string, 0, len(declared))
	for key := range declared {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		export = append(export, key+"="+declared[key])
	}

	protected := make(map[string]bool, len(alreadySet))
	for _, entry := range alreadySet {
		if key, _, found := strings.Cut(entry, "="); found {
			protected[key] = true
		}
	}

	seen := make(map[string]bool)
	for _, entry := range ambient {
		key, _, found := strings.Cut(entry, "=")
		if !found || seen[key] || protected[key] {
			continue
		}
		if _, isDeclared := declared[key]; isDeclared {
			continue
		}
		if !isScopedCredentialEnvironmentKey(key) || serverOwned[key] {
			continue
		}
		seen[key] = true
		unset = append(unset, key)
	}
	sort.Strings(unset)
	return export, unset
}

// ScopedCredentialPrefixes and ScopedCredentialNames expose the credential
// namespace as data so a launch boundary can scrub it dynamically instead of
// from a precomputed list. Keep them in step with
// isScopedCredentialEnvironmentKey -- that function and these are the same
// policy expressed for two consumers (Go filtering, and shell matching).
func ScopedCredentialPrefixes() []string { return []string{"SECRET_", "VAR_"} }

func ScopedCredentialNames() []string {
	return []string{"MCP_API_TOKEN", "MCP_AUTH", "MCP_SESSION_ID"}
}

// CodingAgentScopeFingerprint deterministically identifies the credential
// scope selected for a call, so a RETAINED coding-agent process can be
// detected as running under a stale one.
//
// A retained CLI applies its environment once, at launch. Without this, turn 2
// declaring a different or narrower scope silently kept turn 1's credentials
// alive in the live process, and revoking a credential was impossible without
// killing the session by hand.
//
// Both keys and values are hashed: a changed VALUE for the same key is just as
// much a scope change as a changed key set, and hashing keeps credentials out
// of the session state and any log that prints it. The empty-but-declared
// scope is deliberately distinct from no scope at all, matching
// CodingAgentScopeDeclared.
func CodingAgentScopeFingerprint(opts *CallOptions) string {
	accountEntries := mergeProviderAccountEnvironment(nil, opts)
	accountHash := ""
	if len(accountEntries) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(accountEntries, "\n")))
		accountHash = hex.EncodeToString(sum[:]) + ":"
	}
	if CLIHasPrivateRuntime(opts) {
		sum := sha256.Sum256([]byte(opts.CLIRuntimeDir))
		accountHash += hex.EncodeToString(sum[:]) + ":"
	}
	if !CodingAgentScopeDeclared(opts) {
		return accountHash + "unscoped"
	}
	declared := CodingAgentSecretEnvironmentFromOptions(opts)
	if len(declared) == 0 {
		return accountHash + "scoped-empty"
	}
	keys := make([]string, 0, len(declared))
	for key := range declared {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		// Length-prefixed so ("AB","C") and ("A","BC") cannot collide.
		fmt.Fprintf(hash, "%d:%s=%d:%s\n", len(key), key, len(declared[key]), declared[key])
	}
	return accountHash + hex.EncodeToString(hash.Sum(nil))
}

// MergeCodingAgentSecretEnvironment overlays scoped secrets on a process
// environment without mutating the caller's slice.
//
// When a scoped environment is declared it is AUTHORITATIVE for the credential
// namespace: an ambient SECRET_*/VAR_* the caller did not declare for this
// child is dropped rather than inherited. Overlaying alone made the isolation
// guarantee untrue at the process boundary -- a child could still read whatever
// credentials happened to be in the launcher's environment, which is exactly
// what scoping is supposed to prevent.
//
// The MCP_* prefix splits in two and must not be treated as one thing:
//
//	MCP_API_TOKEN / MCP_AUTH / MCP_SESSION_ID  credentials + session identity
//	MCP_API_URL / MCP_CUSTOM / MCP_MCP / MCP_VIRTUAL  addresses
//
// The first group is scrubbed with the rest of the credential namespace:
// inheriting an ambient bearer token or session id lets a child act with
// another session's authority, which is the same leak as an inherited
// SECRET_*, not harmless routing metadata.
//
// The address routes are still inherited, because this layer has been burned
// once by over-filtering them: a previous version admitted only SECRET_*,
// silently dropped the routes, and the child never saw MCP_CUSTOM with nothing
// erroring (see withCodingAgentSecretEnvironment in mcpagent). Dropping an
// address a caller relies on the launcher to set reproduces that silent
// failure, and an address grants nothing without the credentials above.
func MergeCodingAgentSecretEnvironment(base []string, opts *CallOptions) []string {
	base = mergeProviderAccountEnvironment(base, opts)
	if key := codingAgentReleaseSessionKey(opts); key != "" {
		// Runtime version selection does not declare or broaden credential scope.
		filtered := make([]string, 0, len(base)+1)
		for _, entry := range base {
			if !strings.HasPrefix(entry, "AGENTWORKS_CLI_SESSION_KEY=") {
				filtered = append(filtered, entry)
			}
		}
		base = append(filtered, "AGENTWORKS_CLI_SESSION_KEY="+key)
	}
	if !CodingAgentScopeDeclared(opts) {
		return append([]string(nil), base...)
	}
	secrets := CodingAgentSecretEnvironmentFromOptions(opts)
	out := make([]string, 0, len(base)+len(secrets))
	for _, entry := range base {
		key, _, found := strings.Cut(entry, "=")
		if found && secrets[key] != "" {
			continue
		}
		if found && isScopedCredentialEnvironmentKey(key) {
			// Declared but absent from this child's scope => not for it.
			continue
		}
		out = append(out, entry)
	}
	keys := make([]string, 0, len(secrets))
	for key := range secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+secrets[key])
	}
	return out
}

// WithStreamingFunc is a convenience function that creates a channel and callback
// This maintains backward compatibility for simple use cases
// For better control, use WithStreamingChan directly
func WithStreamingFunc(fn func(StreamChunk)) CallOption {
	ch := make(chan StreamChunk, 100) // Buffered channel to avoid blocking
	go func() {
		for chunk := range ch {
			fn(chunk)
		}
	}()
	return WithStreamingChan(ch)
}

// TextPart creates a single text part message content
func TextPart(role ChatMessageType, text string) MessageContent {
	return MessageContent{
		Role:  role,
		Parts: []ContentPart{TextContent{Text: text}},
	}
}

// TextParts creates a message content with multiple text parts
func TextParts(role ChatMessageType, texts ...string) MessageContent {
	parts := make([]ContentPart, len(texts))
	for i, text := range texts {
		parts[i] = TextContent{Text: text}
	}
	return MessageContent{
		Role:  role,
		Parts: parts,
	}
}

// ImagePart creates a message content with a single image part
// sourceType should be "base64" or "url"
// For base64: mediaType is required (e.g., "image/jpeg"), data is base64-encoded string
// For url: mediaType is ignored, data is the image URL
func ImagePart(role ChatMessageType, sourceType, mediaType, data string) MessageContent {
	return MessageContent{
		Role: role,
		Parts: []ContentPart{
			ImageContent{
				SourceType: sourceType,
				MediaType:  mediaType,
				Data:       data,
			},
		},
	}
}

// ImagePartBase64 creates a message content with a base64-encoded image
func ImagePartBase64(role ChatMessageType, mediaType, base64Data string) MessageContent {
	return ImagePart(role, "base64", mediaType, base64Data)
}

// ImagePartURL creates a message content with an image URL
func ImagePartURL(role ChatMessageType, imageURL string) MessageContent {
	return ImagePart(role, "url", "", imageURL)
}

// WithEmbeddingModel sets the embedding model ID
func WithEmbeddingModel(model string) EmbeddingOption {
	return func(opts *EmbeddingOptions) {
		opts.Model = model
	}
}

// WithDimensions sets the dimensions parameter for embedding generation
// This is only supported for text-embedding-3 models
func WithDimensions(dimensions int) EmbeddingOption {
	return func(opts *EmbeddingOptions) {
		opts.Dimensions = &dimensions
	}
}

// WithReasoningEffort sets the reasoning effort level for models that support it (e.g., gpt-5.1)
// Valid values: "minimal", "low", "medium", "high"
// When set to "minimal", the model uses minimal reasoning effort
// Higher values enable deeper reasoning for complex problems
func WithReasoningEffort(effort string) CallOption {
	return func(opts *CallOptions) {
		opts.ReasoningEffort = effort
	}
}

// WithVerbosity sets the verbosity level for the model's response (for reasoning models)
// Valid values: "low", "medium", "high"
// Lower values result in more concise responses, higher values result in more verbose responses
func WithVerbosity(verbosity string) CallOption {
	return func(opts *CallOptions) {
		opts.Verbosity = verbosity
	}
}

// WithThinkingLevel sets the thinking level for models that support it (e.g., Gemini 3 Pro)
// Valid values: "low", "high"
// "low" reduces latency for simpler tasks, "high" enables deeper reasoning for complex tasks.
// Default is "high" for Gemini 3 Pro.
func WithThinkingLevel(level string) CallOption {
	return func(opts *CallOptions) {
		opts.ThinkingLevel = level
	}
}

// WithThinkingBudget sets the thinking budget (token limit) for models that support it
// (e.g., Gemini 2.5 Flash Thinking)
func WithThinkingBudget(budget int) CallOption {
	return func(opts *CallOptions) {
		opts.ThinkingBudget = budget
	}
}

// WithAllowedTools sets the list of explicitly allowed tools for the model (e.g., gpt-5.2-codex)
func WithAllowedTools(tools []string) CallOption {
	return func(opts *CallOptions) {
		opts.AllowedTools = tools
	}
}
