package agentprofiles

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu              sync.RWMutex
	profiles        map[string]map[int]Profile
	factories       map[string]ToolFactory
	initializers    map[string]RuntimeInitializer
	promptVariables map[string]PromptVariablesProvider
	channelRouters  map[string]ChannelRouter
}

func NewRegistry() *Registry {
	return &Registry{
		profiles:     make(map[string]map[int]Profile),
		factories:    make(map[string]ToolFactory),
		initializers: make(map[string]RuntimeInitializer),
	}
}

func (r *Registry) RegisterProfile(profile Profile) error {
	if r == nil {
		return fmt.Errorf("profile registry is nil")
	}
	if err := ResolveFeatures(&profile); err != nil {
		return err
	}
	if err := Validate(profile); err != nil {
		return err
	}
	profile = cloneProfile(profile)

	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.profiles[profile.ID]
	if versions == nil {
		versions = make(map[int]Profile)
		r.profiles[profile.ID] = versions
	}
	if _, exists := versions[profile.Version]; exists {
		return fmt.Errorf("profile %q version %d is already registered", profile.ID, profile.Version)
	}
	for _, existing := range versions {
		if existing.BuiltIn != profile.BuiltIn || existing.OwnerID != profile.OwnerID {
			return fmt.Errorf("profile %q ownership cannot change across versions", profile.ID)
		}
	}
	versions[profile.Version] = profile
	return nil
}

func (r *Registry) Resolve(id string, version int, userID string) (Profile, error) {
	if r == nil {
		return Profile{}, fmt.Errorf("profile registry is nil")
	}
	id = strings.TrimSpace(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := r.profiles[id]
	if len(versions) == 0 {
		return Profile{}, fmt.Errorf("profile %q not found", id)
	}
	if version == 0 {
		for candidate := range versions {
			if candidate > version {
				version = candidate
			}
		}
	}
	profile, exists := versions[version]
	if !exists {
		return Profile{}, fmt.Errorf("profile %q version %d not found", id, version)
	}
	if !profile.BuiltIn && strings.TrimSpace(profile.OwnerID) != strings.TrimSpace(userID) {
		return Profile{}, fmt.Errorf("profile %q is not available to this user", id)
	}
	return cloneProfile(profile), nil
}

func (r *Registry) List(userID string) []Profile {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	profiles := make([]Profile, 0)
	for _, versions := range r.profiles {
		for _, profile := range versions {
			if profile.BuiltIn || profile.OwnerID == userID {
				profiles = append(profiles, cloneProfile(profile))
			}
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].ID == profiles[j].ID {
			return profiles[i].Version < profiles[j].Version
		}
		return profiles[i].ID < profiles[j].ID
	})
	return profiles
}

// HasToolFactory lets shared platform capabilities register once when several
// installed product profiles bind the same implementation.
func (r *Registry) HasToolFactory(id string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.factories[strings.TrimSpace(id)] != nil
}

func (r *Registry) RegisterToolFactory(id string, factory ToolFactory) error {
	if r == nil {
		return fmt.Errorf("profile registry is nil")
	}
	id = strings.TrimSpace(id)
	if !toolIDPattern.MatchString(id) {
		return fmt.Errorf("invalid tool factory id %q", id)
	}
	if factory == nil {
		return fmt.Errorf("tool factory %q is nil", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[id]; exists {
		return fmt.Errorf("tool factory %q is already registered", id)
	}
	r.factories[id] = factory
	return nil
}

// RegisterPromptVariables attaches a per-turn prompt variable provider to a
// profile (see PromptContext.Product).
func (r *Registry) RegisterPromptVariables(profileID string, provider PromptVariablesProvider) error {
	if r == nil {
		return fmt.Errorf("profile registry is nil")
	}
	profileID = strings.TrimSpace(profileID)
	if !profileIDPattern.MatchString(profileID) {
		return fmt.Errorf("invalid profile id %q", profileID)
	}
	if provider == nil {
		return fmt.Errorf("prompt variables provider %q is nil", profileID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.promptVariables == nil {
		r.promptVariables = map[string]PromptVariablesProvider{}
	}
	if _, exists := r.promptVariables[profileID]; exists {
		return fmt.Errorf("prompt variables provider %q is already registered", profileID)
	}
	r.promptVariables[profileID] = provider
	return nil
}

// PromptVariables runs the profile's provider, or returns nil when none is
// registered.
func (r *Registry) PromptVariables(ctx context.Context, profileID string, runtime RuntimeContext) (map[string]string, error) {
	if r == nil {
		return nil, fmt.Errorf("profile registry is nil")
	}
	r.mu.RLock()
	provider := r.promptVariables[strings.TrimSpace(profileID)]
	r.mu.RUnlock()
	if provider == nil {
		return nil, nil
	}
	return provider(ctx, runtime)
}

// RegisterChannelRouter attaches a product's @token router to the profile
// that fronts the product on a channel (the one a pairing names as its
// default destination). Tokens the router does not know fall through to the
// platform's own routing.
func (r *Registry) RegisterChannelRouter(profileID string, router ChannelRouter) error {
	if r == nil {
		return fmt.Errorf("profile registry is nil")
	}
	profileID = strings.TrimSpace(profileID)
	if !profileIDPattern.MatchString(profileID) {
		return fmt.Errorf("invalid profile id %q", profileID)
	}
	if router == nil {
		return fmt.Errorf("channel router %q is nil", profileID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.channelRouters == nil {
		r.channelRouters = map[string]ChannelRouter{}
	}
	if _, exists := r.channelRouters[profileID]; exists {
		return fmt.Errorf("channel router %q is already registered", profileID)
	}
	r.channelRouters[profileID] = router
	return nil
}

// ResolveChannelRoute asks the profile's product router about a token;
// ok=false when no router is registered or the token is not the product's.
func (r *Registry) ResolveChannelRoute(ctx context.Context, profileID, userID, token string) (ChannelProfileRoute, bool, error) {
	if r == nil {
		return ChannelProfileRoute{}, false, fmt.Errorf("profile registry is nil")
	}
	r.mu.RLock()
	router := r.channelRouters[strings.TrimSpace(profileID)]
	r.mu.RUnlock()
	if router == nil {
		return ChannelProfileRoute{}, false, nil
	}
	return router(ctx, userID, strings.ToLower(strings.TrimSpace(token)))
}

func (r *Registry) RegisterInitializer(profileID string, initializer RuntimeInitializer) error {
	if r == nil {
		return fmt.Errorf("profile registry is nil")
	}
	profileID = strings.TrimSpace(profileID)
	if !profileIDPattern.MatchString(profileID) {
		return fmt.Errorf("invalid profile id %q", profileID)
	}
	if initializer == nil {
		return fmt.Errorf("profile initializer %q is nil", profileID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.initializers[profileID]; exists {
		return fmt.Errorf("profile initializer %q is already registered", profileID)
	}
	r.initializers[profileID] = initializer
	return nil
}

func (r *Registry) Initialize(ctx context.Context, profileID string, runtime RuntimeContext) error {
	if r == nil {
		return fmt.Errorf("profile registry is nil")
	}
	r.mu.RLock()
	initializer := r.initializers[strings.TrimSpace(profileID)]
	r.mu.RUnlock()
	if initializer == nil {
		return nil
	}
	return initializer(ctx, runtime)
}

func (r *Registry) BuildTool(binding ToolBinding, runtime ToolRuntimeContext) (ToolSpec, error) {
	if r == nil {
		return ToolSpec{}, fmt.Errorf("profile registry is nil")
	}
	r.mu.RLock()
	factory := r.factories[strings.TrimSpace(binding.ID)]
	r.mu.RUnlock()
	if factory == nil {
		return ToolSpec{}, fmt.Errorf("tool factory %q is not registered", binding.ID)
	}
	config := append(json.RawMessage(nil), binding.Config...)
	return factory(runtime, config)
}

func cloneProfile(profile Profile) Profile {
	cloned := profile
	cloned.Features = make([]FeatureBinding, len(profile.Features))
	for i, binding := range profile.Features {
		cloned.Features[i] = binding
		cloned.Features[i].Options = cloneStringMap(binding.Options)
		if binding.Enabled != nil {
			enabled := *binding.Enabled
			cloned.Features[i].Enabled = &enabled
		}
	}
	cloned.ResolvedFeatures = make([]ResolvedFeature, len(profile.ResolvedFeatures))
	for i, feature := range profile.ResolvedFeatures {
		cloned.ResolvedFeatures[i] = feature
		cloned.ResolvedFeatures[i].Dependencies = cloneStrings(feature.Dependencies)
		cloned.ResolvedFeatures[i].Tools = cloneStrings(feature.Tools)
		cloned.ResolvedFeatures[i].Skills = cloneStrings(feature.Skills)
		cloned.ResolvedFeatures[i].UIPanels = cloneStrings(feature.UIPanels)
		cloned.ResolvedFeatures[i].Capabilities = cloneCapabilities(feature.Capabilities)
		cloned.ResolvedFeatures[i].Options = cloneStringMap(feature.Options)
	}
	cloned.Skills = append([]string(nil), profile.Skills...)
	cloned.Commands = append([]CommandBinding(nil), profile.Commands...)
	cloned.Secrets = append([]SecretBinding(nil), profile.Secrets...)
	cloned.Tools = make([]ToolBinding, len(profile.Tools))
	for i, binding := range profile.Tools {
		cloned.Tools[i] = binding
		cloned.Tools[i].Config = append(json.RawMessage(nil), binding.Config...)
		if binding.Presentation != nil {
			presentation := *binding.Presentation
			if binding.Presentation.Activity != nil {
				activity := *binding.Presentation.Activity
				presentation.Activity = &activity
			}
			cloned.Tools[i].Presentation = &presentation
		}
	}
	cloned.ToolPolicy.Enabled = append([]string(nil), profile.ToolPolicy.Enabled...)
	cloned.ToolPolicy.Disabled = append([]string(nil), profile.ToolPolicy.Disabled...)
	return cloned
}
