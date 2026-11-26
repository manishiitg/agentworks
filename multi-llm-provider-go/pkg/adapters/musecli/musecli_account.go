package musecli

import (
	"context"
	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
)

type museAccountContextKey struct{}
type museAccountRuntime struct {
	opts   *llmtypes.CallOptions
	apiKey string
}

func museWithAccount(ctx context.Context, opts *llmtypes.CallOptions, apiKey string) context.Context {
	return context.WithValue(ctx, museAccountContextKey{}, museAccountRuntime{opts, apiKey})
}
func museAccount(ctx context.Context) museAccountRuntime {
	value, _ := ctx.Value(museAccountContextKey{}).(museAccountRuntime)
	return value
}
func museAccountDataHome(ctx context.Context) string {
	// A confined Muse keeps its sessions in its private home.
	if root := llmtypes.CLIHomeEnvironment(museAccount(ctx).opts)["XDG_DATA_HOME"]; root != "" {
		return root
	}
	return museXDGDataHome()
}
func museAccountConfig(ctx context.Context, mcpJSON string, tools []string) (string, func(), error) {
	source := ""
	if root := llmtypes.ProviderAccountEnvironment(museAccount(ctx).opts)["XDG_CONFIG_HOME"]; root != "" {
		source = filepath.Join(root, "muse", "settings.json")
	}
	configHome, cleanup, err := musePrepareIsolatedConfig(mcpJSON, tools, source)
	if err != nil {
		return configHome, cleanup, err
	}
	// The user's own Linux account runs Muse: it needs to read and write the config folder just built.
	if opts := museAccount(ctx).opts; opts != nil && opts.CLISecurity != nil {
		if shareErr := slotfs.ShareTree(opts.CLISecurity.PrivateHome, configHome); shareErr != nil {
			cleanup()
			return "", nil, shareErr
		}
	}
	return configHome, cleanup, nil
}

// museBaseURLArgv points Muse at the account's Meta API address, when the account has one.
func museBaseURLArgv(opts *llmtypes.CallOptions) []string {
	if url := llmtypes.ProviderAccountEnvironment(opts)[llmtypes.MuseBaseURLKey]; url != "" {
		return []string{"--base-url", url}
	}
	return nil
}
func museAccountLaunch(ctx context.Context, argv []string, workdir string) (string, func(), error) {
	runtime := museAccount(ctx)
	argv = append(argv, museBaseURLArgv(runtime.opts)...)
	env, unset := llmtypes.ScopedCodingAgentEnvironmentPlan(os.Environ(), nil, runtime.opts)
	env = append(env, museRuntimeEnv(runtime.opts)...)
	argv = musePreludeArgv(runtime.opts, argv)
	// A selected key must override ambient login credentials without appearing in argv.
	if runtime.apiKey != "" {
		env = append(env, "META_API_KEY="+runtime.apiKey)
		unset = append(unset, "META_API_KEY")
	}
	argv, cleanupSandbox, err := museLandlockArgs(runtime.opts, argv, workdir)
	if err != nil {
		return "", nil, err
	}
	command, cleanup, err := shelllaunch.CommandWithScopedEnv(argv, workdir, env, unset, nil)
	if err != nil {
		cleanupSandbox()
		return "", nil, err
	}
	return command, func() { cleanup(); cleanupSandbox() }, nil
}
