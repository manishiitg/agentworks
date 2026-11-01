package clisecurity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const CurrentConfigVersion = 1

var ErrModeNotEnforceable = errors.New("CLI security mode is not yet enforceable")

// UserConfig is server-controlled operator state. It must not be stored in an
// agent-editable workflow or workspace directory.
type UserConfig struct {
	Version          int                        `json:"version"`
	Mode             llmtypes.CLISecurityMode   `json:"mode"`
	ApprovedProfiles map[string]ProfileApproval `json:"approved_profiles,omitempty"`
}

type ProfileApproval struct {
	ProfileVersion string   `json:"profile_version"`
	Capabilities   []string `json:"capabilities"`
}

type Store struct {
	root string
	mu   sync.Mutex
}

// DefaultRoot resolves the machine-wide CLI-security store under AgentWorks.
// AGENTWORKS_CLI_SECURITY_DIR gives an isolated desktop instance its own root.
func DefaultRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AGENTWORKS_CLI_SECURITY_DIR")); override != "" {
		return filepath.Clean(override), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "AgentWorks", "cli-security"), nil
}

func NewStore(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("CLI security store root is required")
	}
	return &Store{root: filepath.Clean(root)}, nil
}

func DefaultConfig() UserConfig {
	return UserConfig{
		Version:          CurrentConfigVersion,
		Mode:             llmtypes.CLISecurityModeCompatibility,
		ApprovedProfiles: map[string]ProfileApproval{},
	}
}

func (s *Store) Read(userID string) (UserConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readUnlocked(userID)
}

func (s *Store) Write(userID string, config UserConfig) (UserConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized, err := validateConfig(config)
	if err != nil {
		return UserConfig{}, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return UserConfig{}, fmt.Errorf("create CLI security config directory: %w", err)
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return UserConfig{}, fmt.Errorf("encode CLI security config: %w", err)
	}
	data = append(data, '\n')
	target := s.userPath(userID)
	temp, err := os.CreateTemp(s.root, ".cli-security-*.tmp")
	if err != nil {
		return UserConfig{}, fmt.Errorf("create temporary CLI security config: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return UserConfig{}, fmt.Errorf("protect temporary CLI security config: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return UserConfig{}, fmt.Errorf("write temporary CLI security config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return UserConfig{}, fmt.Errorf("sync temporary CLI security config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return UserConfig{}, fmt.Errorf("close temporary CLI security config: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return UserConfig{}, fmt.Errorf("replace CLI security config: %w", err)
	}
	return cloneConfig(normalized), nil
}

func (s *Store) Resolve(userID, provider string, workspaceReadPaths, workspaceWritePaths []string) (*llmtypes.CLISecurityPolicy, error) {
	config, err := s.Read(userID)
	if err != nil {
		return nil, err
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                config.Mode,
		Provider:            strings.TrimSpace(provider),
		WorkspaceReadPaths:  append([]string(nil), workspaceReadPaths...),
		WorkspaceWritePaths: append([]string(nil), workspaceWritePaths...),
	}
	if config.Mode == llmtypes.CLISecurityModeCompatibility {
		resolved := policy.Clone()
		return &resolved, nil
	}

	profile, ok := llmproviders.GetCodingAgentSecurityProfile(llmproviders.Provider(provider))
	if !ok || !profile.Certified {
		return nil, fmt.Errorf("%w: provider %q has no certified profile", ErrModeNotEnforceable, provider)
	}
	policy.ProfileVersion = profile.Version
	if config.Mode == llmtypes.CLISecurityModeVerified {
		approval, approved := config.ApprovedProfiles[string(profile.Provider)]
		if !approved || approval.ProfileVersion != profile.Version {
			return nil, fmt.Errorf("%w: provider profile %s@%s is not approved", ErrModeNotEnforceable, profile.Provider, profile.Version)
		}
		capabilities := make(map[string]llmproviders.CodingAgentSecurityCapability, len(profile.Capabilities))
		for _, capability := range profile.Capabilities {
			capabilities[capability.ID] = capability
		}
		for _, capabilityID := range approval.Capabilities {
			capability, exists := capabilities[capabilityID]
			if !exists {
				return nil, fmt.Errorf("%w: unknown capability %q for %s@%s", ErrModeNotEnforceable, capabilityID, profile.Provider, profile.Version)
			}
			policy.ApprovedCapabilities = append(policy.ApprovedCapabilities, capabilityID)
			policy.HostReadPaths = append(policy.HostReadPaths, expandPathTemplates(capability.ReadPathTemplates)...)
			policy.HostWritePaths = append(policy.HostWritePaths, expandPathTemplates(capability.WritePathTemplates)...)
			policy.EnvironmentVariables = append(policy.EnvironmentVariables, capability.Environment...)
			delete(capabilities, capabilityID)
		}
		if len(capabilities) != 0 {
			return nil, fmt.Errorf("%w: provider %s requires approval of every baseline capability", ErrModeNotEnforceable, profile.Provider)
		}
	} else {
		policy.PrivateHome = filepath.Join(
			s.root,
			"environments",
			userKey(userID),
			string(profile.Provider),
			"v"+profile.Version,
		)
	}
	resolved := policy.Clone()
	return &resolved, nil
}

func (s *Store) readUnlocked(userID string) (UserConfig, error) {
	data, err := os.ReadFile(s.userPath(userID))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return UserConfig{}, fmt.Errorf("read CLI security config: %w", err)
	}
	var config UserConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return UserConfig{}, fmt.Errorf("decode CLI security config: %w", err)
	}
	return validateConfig(config)
}

func validateConfig(config UserConfig) (UserConfig, error) {
	switch llmtypes.CLISecurityMode(strings.ToLower(strings.TrimSpace(string(config.Mode)))) {
	case "":
		config.Mode = llmtypes.CLISecurityModeCompatibility
	case llmtypes.CLISecurityModeCompatibility:
		config.Mode = llmtypes.CLISecurityModeCompatibility
	case llmtypes.CLISecurityModeIsolated:
		config.Mode = llmtypes.CLISecurityModeIsolated
	case llmtypes.CLISecurityModeVerified:
		config.Mode = llmtypes.CLISecurityModeVerified
	default:
		return UserConfig{}, fmt.Errorf("unsupported CLI security mode %q", config.Mode)
	}
	if config.Version == 0 {
		config.Version = CurrentConfigVersion
	}
	if config.Version != CurrentConfigVersion {
		return UserConfig{}, fmt.Errorf("unsupported CLI security config version %d", config.Version)
	}
	if config.ApprovedProfiles == nil {
		config.ApprovedProfiles = map[string]ProfileApproval{}
	}
	return cloneConfig(config), nil
}

func cloneConfig(config UserConfig) UserConfig {
	out := config
	out.ApprovedProfiles = make(map[string]ProfileApproval, len(config.ApprovedProfiles))
	for provider, approval := range config.ApprovedProfiles {
		approval.Capabilities = append([]string(nil), approval.Capabilities...)
		out.ApprovedProfiles[provider] = approval
	}
	return out
}

func (s *Store) userPath(userID string) string {
	return filepath.Join(s.root, userKey(userID)+".json")
}

func userKey(userID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(userID)))
	return hex.EncodeToString(sum[:])
}

func expandPathTemplates(templates []string) []string {
	home, _ := os.UserHomeDir()
	paths := make([]string, 0, len(templates))
	for _, template := range templates {
		path := strings.TrimSpace(template)
		if strings.HasPrefix(path, "~/") && home != "" {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
		if path != "" {
			paths = append(paths, filepath.Clean(path))
		}
	}
	return paths
}
