package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var imageGenProvidersTestCmd = &cobra.Command{
	Use:   "image-gen-providers",
	Short: "Test image_gen across supported generation providers",
	Long: `Test the provider-backed image_gen path across supported generation providers.

This command calls the real image_gen executor directly, so it exercises
workspace saving, provider auth, CLI runtime availability, and the provider's
native image generation implementation.`,
	RunE: runImageGenProvidersTest,
}

type imageGenProviderResult struct {
	Model      string   `json:"model"`
	Prompt     string   `json:"prompt"`
	SavedPaths []string `json:"saved_paths"`
	Count      int      `json:"count"`
}

func runImageGenProvidersTest(cmd *cobra.Command, args []string) error {
	loadTestingEnvFiles()

	logFile := viper.GetString("log-file")
	logLevel := viper.GetString("log-level")
	InitTestLogger(logFile, logLevel)
	logger := GetTestLogger()

	workspaceURL := strings.TrimSpace(viper.GetString("image-gen-providers.workspace-url"))
	if workspaceURL == "" {
		workspaceURL = strings.TrimSpace(os.Getenv("WORKSPACE_API_URL"))
	}
	if workspaceURL == "" {
		workspaceURL = "http://127.0.0.1:8081"
	}
	if err := os.Setenv("WORKSPACE_API_URL", workspaceURL); err != nil {
		return fmt.Errorf("failed to set WORKSPACE_API_URL: %w", err)
	}

	userID := strings.TrimSpace(viper.GetString("image-gen-providers.user-id"))
	if userID == "" {
		userID = "default"
	}

	prompt := strings.TrimSpace(viper.GetString("image-gen-providers.prompt"))
	if prompt == "" {
		prompt = "A simple red square icon centered on a white background"
	}
	aspectRatio := strings.TrimSpace(viper.GetString("image-gen-providers.aspect-ratio"))
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}

	timeoutValue := strings.TrimSpace(viper.GetString("image-gen-providers.provider-timeout"))
	providerTimeout, err := time.ParseDuration(timeoutValue)
	if err != nil {
		return fmt.Errorf("invalid --provider-timeout %q: %w", timeoutValue, err)
	}

	modelOverrides := parseProviderModelOverrides(viper.GetString("image-gen-providers.models"))
	providers := parseCSVList(viper.GetString("image-gen-providers.providers"))
	if len(providers) == 0 {
		providers = []string{"vertex", "codex-cli"}
	}

	keys, err := loadWorkspaceProviderKeys(context.Background(), workspaceURL)
	if err != nil {
		logger.Warn(fmt.Sprintf("Provider key store unavailable; falling back to env/CLI auth only: %v", err))
	}

	executor := virtualtools.CreateImageGenExecutor(virtualtools.ImageGenExecutorConfig{
		WorkspaceAPIURL: workspaceURL,
		UserID:          userID,
	})
	if executor == nil {
		return fmt.Errorf("image_gen executor is not available")
	}

	includeUnconfigured := viper.GetBool("image-gen-providers.include-unconfigured")
	failFast := viper.GetBool("image-gen-providers.fail-fast")
	outputFolder := strings.TrimSpace(viper.GetString("image-gen-providers.output-folder"))
	if outputFolder == "" {
		outputFolder = fmt.Sprintf("_users/%s/Chats/generated-image-provider-tests", userID)
	}
	outputFolder = strings.Trim(path.Clean(outputFolder), "/")
	runID := time.Now().Unix()

	var passed, skipped, failed int
	var failures []string
	fmt.Printf("Testing image_gen providers\n")
	fmt.Printf("Workspace URL: %s\n", workspaceURL)
	fmt.Printf("User ID: %s\n", userID)
	fmt.Printf("Prompt: %s\n\n", prompt)

	wsClient := workspace.NewClient(workspaceURL, workspace.WithUserID(userID))

	for _, provider := range providers {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" {
			continue
		}
		modelID := strings.TrimSpace(modelOverrides[provider])
		if modelID == "" {
			modelID = defaultImageGenProviderModel(provider)
		}
		if modelID == "" {
			failed++
			msg := fmt.Sprintf("%s: no default image generation model known; pass --models %s=<model>", provider, provider)
			failures = append(failures, msg)
			fmt.Printf("[FAIL] %s\n", msg)
			if failFast {
				break
			}
			continue
		}

		apiKey := imageGenProviderAPIKey(provider, keys)
		if !includeUnconfigured {
			if reason := imageGenProviderSkipReason(provider, apiKey); reason != "" {
				skipped++
				fmt.Printf("[SKIP] %s/%s: %s\n", provider, modelID, reason)
				continue
			}
		}

		outputPath := workspaceDocsAbsoluteTestPath(fmt.Sprintf("%s/%s-%d", outputFolder, strings.ReplaceAll(provider, "-", "_"), runID))
		ctx, cancel := context.WithTimeout(context.Background(), providerTimeout)
		ctx = context.WithValue(ctx, common.FolderGuardAllowedWriteFolderKey, []string{outputFolder})
		ctx = context.WithValue(ctx, common.FolderGuardReadPathsKey, []string{outputFolder})
		callArgs := map[string]any{
			"prompt":           prompt,
			"provider":         provider,
			"model_id":         modelID,
			"output_path":      outputPath,
			"aspect_ratio":     aspectRatio,
			"number_of_images": float64(1),
		}

		start := time.Now()
		result, err := executor(ctx, callArgs)

		if err != nil {
			cancel()
			failed++
			msg := fmt.Sprintf("%s/%s failed after %s: %v", provider, modelID, time.Since(start).Round(time.Millisecond), err)
			failures = append(failures, msg)
			fmt.Printf("[FAIL] %s\n", msg)
			if failFast {
				break
			}
			continue
		}

		var parsed imageGenProviderResult
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			cancel()
			failed++
			msg := fmt.Sprintf("%s/%s returned invalid JSON: %v", provider, modelID, err)
			failures = append(failures, msg)
			fmt.Printf("[FAIL] %s\n", msg)
			if failFast {
				break
			}
			continue
		}
		if parsed.Count < 1 || len(parsed.SavedPaths) < 1 {
			cancel()
			failed++
			msg := fmt.Sprintf("%s/%s returned no saved image paths: %s", provider, modelID, oneLinePreview(result, 220))
			failures = append(failures, msg)
			fmt.Printf("[FAIL] %s\n", msg)
			if failFast {
				break
			}
			continue
		}
		if err := verifyGeneratedImageFiles(ctx, wsClient, parsed.SavedPaths); err != nil {
			cancel()
			failed++
			msg := fmt.Sprintf("%s/%s generated invalid image output: %v", provider, modelID, err)
			failures = append(failures, msg)
			fmt.Printf("[FAIL] %s\n", msg)
			if failFast {
				break
			}
			continue
		}

		cancel()
		passed++
		fmt.Printf("[PASS] %s/%s in %s: %s\n", provider, modelID, time.Since(start).Round(time.Millisecond), strings.Join(parsed.SavedPaths, ", "))
	}

	fmt.Printf("\nSummary: %d passed, %d skipped, %d failed\n", passed, skipped, failed)
	if len(failures) > 0 {
		sort.Strings(failures)
		for _, failure := range failures {
			fmt.Printf("- %s\n", failure)
		}
		return fmt.Errorf("image_gen provider matrix had %d failure(s)", failed)
	}
	return nil
}

func defaultImageGenProviderModel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "vertex":
		return "gemini-3.1-flash-image"
	case "codex-cli":
		return "codex-cli"
	default:
		return ""
	}
}

func imageGenProviderAPIKey(provider string, keys map[string]string) *string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	candidates := map[string][]string{
		"vertex":    {"vertex"},
		"codex-cli": {"codex_cli"},
	}[provider]
	for _, keyName := range candidates {
		if keys != nil {
			if value := strings.TrimSpace(keys[keyName]); value != "" {
				return &value
			}
		}
	}
	envCandidates := map[string][]string{
		"vertex":    {"VERTEX_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY"},
		"codex-cli": {"CODEX_API_KEY"},
	}[provider]
	for _, envName := range envCandidates {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			return &value
		}
	}
	return nil
}

func imageGenProviderSkipReason(provider string, apiKey *string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "vertex":
		if apiKey == nil && strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")) == "" && strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT")) == "" {
			return "no Vertex/Gemini API key or Google ADC environment found"
		}
	case "codex-cli":
		if _, err := exec.LookPath("codex"); err != nil {
			return "codex CLI is not installed or not on PATH"
		}
	default:
		return "unsupported image_gen provider"
	}
	return ""
}

func verifyGeneratedImageFiles(ctx context.Context, client *workspace.Client, paths []string) error {
	for _, savedPath := range paths {
		savedPath = strings.TrimSpace(savedPath)
		if savedPath == "" {
			return fmt.Errorf("empty saved path")
		}
		data, err := client.DownloadFile(ctx, savedPath)
		if err != nil {
			return fmt.Errorf("download %q: %w", savedPath, err)
		}
		if len(data) == 0 {
			return fmt.Errorf("download %q returned empty file", savedPath)
		}
		contentType := http.DetectContentType(data)
		if !strings.HasPrefix(contentType, "image/") {
			return fmt.Errorf("%q detected content type %q, want image/*", savedPath, contentType)
		}
	}
	return nil
}

func init() {
	imageGenProvidersTestCmd.Flags().String("workspace-url", "", "Workspace API URL (default: WORKSPACE_API_URL or http://127.0.0.1:8081)")
	imageGenProvidersTestCmd.Flags().String("user-id", "default", "Workspace user ID to use when saving generated images")
	imageGenProvidersTestCmd.Flags().String("prompt", "", "Prompt to use for image generation")
	imageGenProvidersTestCmd.Flags().String("aspect-ratio", "1:1", "Aspect ratio to request")
	imageGenProvidersTestCmd.Flags().String("providers", "", "Comma-separated providers to test (default: vertex,codex-cli)")
	imageGenProvidersTestCmd.Flags().String("models", "", "Comma-separated provider=model overrides, e.g. vertex=gemini-3.1-flash-image,codex-cli=codex-cli")
	imageGenProvidersTestCmd.Flags().String("output-folder", "", "Workspace-docs-relative folder used to build the absolute output_path for generated test images")
	imageGenProvidersTestCmd.Flags().String("provider-timeout", "4m", "Timeout per provider")
	imageGenProvidersTestCmd.Flags().Bool("include-unconfigured", false, "Attempt providers even when auth/runtime preflight is missing")
	imageGenProvidersTestCmd.Flags().Bool("fail-fast", false, "Stop after the first provider failure")

	viper.BindPFlag("image-gen-providers.workspace-url", imageGenProvidersTestCmd.Flags().Lookup("workspace-url"))
	viper.BindPFlag("image-gen-providers.user-id", imageGenProvidersTestCmd.Flags().Lookup("user-id"))
	viper.BindPFlag("image-gen-providers.prompt", imageGenProvidersTestCmd.Flags().Lookup("prompt"))
	viper.BindPFlag("image-gen-providers.aspect-ratio", imageGenProvidersTestCmd.Flags().Lookup("aspect-ratio"))
	viper.BindPFlag("image-gen-providers.providers", imageGenProvidersTestCmd.Flags().Lookup("providers"))
	viper.BindPFlag("image-gen-providers.models", imageGenProvidersTestCmd.Flags().Lookup("models"))
	viper.BindPFlag("image-gen-providers.output-folder", imageGenProvidersTestCmd.Flags().Lookup("output-folder"))
	viper.BindPFlag("image-gen-providers.provider-timeout", imageGenProvidersTestCmd.Flags().Lookup("provider-timeout"))
	viper.BindPFlag("image-gen-providers.include-unconfigured", imageGenProvidersTestCmd.Flags().Lookup("include-unconfigured"))
	viper.BindPFlag("image-gen-providers.fail-fast", imageGenProvidersTestCmd.Flags().Lookup("fail-fast"))
}
