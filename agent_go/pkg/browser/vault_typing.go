package browser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

const browserSecretEnvPrefix = "SECRET_AGENTWORKS_BROWSER_INPUT_"

// browserSecretRefPattern matches $SECRET_<NAME> and ${SECRET_<NAME>} tokens,
// including malformed empty-name forms ($SECRET_ / ${SECRET_}) so they fail
// explicitly instead of typing the placeholder literally.
var browserSecretRefPattern = regexp.MustCompile(`\$\{SECRET_([A-Za-z0-9_]*)\}|\$SECRET_([A-Za-z0-9_]*)`)

type browserSecretPlan struct {
	RewrittenArgs []string
	ExtraEnv      map[string]string
	EnvVarNames   map[string]bool
	RedactValues  []string
}

// prepareBrowserSecretArgs resolves $SECRET_<NAME> (and ${SECRET_<NAME>}) references
// in fill and type arguments using only this turn's selected keys. Resolved values
// travel in ExtraEnv and are referenced as double-quoted "$SECRET_AGENTWORKS_BROWSER_INPUT_N"
// in the shell command so the secret value never enters the command string, process
// registry, or logs.
func prepareBrowserSecretArgs(command string, args []string, secrets map[string]string) (*browserSecretPlan, error) {
	if command != "fill" && command != "type" {
		return nil, nil
	}
	var plan *browserSecretPlan
	rewritten := append([]string(nil), args...)
	redactSet := map[string]struct{}{}
	envIdx := 0

	for i, arg := range args {
		matches := browserSecretRefPattern.FindAllStringSubmatchIndex(arg, -1)
		if len(matches) == 0 {
			continue
		}
		var out strings.Builder
		last := 0
		for _, m := range matches {
			out.WriteString(arg[last:m[0]])
			last = m[1]

			name := ""
			if m[2] >= 0 && m[3] >= 0 {
				name = arg[m[2]:m[3]]
			} else if m[4] >= 0 && m[5] >= 0 {
				name = arg[m[4]:m[5]]
			}
			if name == "" || strings.HasPrefix(name, "AGENTWORKS_BROWSER_INPUT_") {
				return nil, fmt.Errorf("Invalid secret reference in browser %s: use $SECRET_<NAME> (for example $SECRET_SITE_PASSWORD).", command)
			}
			value, ok := secrets["SECRET_"+name]
			if !ok || value == "" {
				return nil, fmt.Errorf("Secret %q is not selected for this turn. Select it for this project in Vault, pass it in browser_secrets, or save it with vault action=set and try again.", name)
			}
			out.WriteString(value)
			collectRedactVariants(redactSet, value)
		}
		out.WriteString(arg[last:])
		resolved := out.String()
		collectRedactVariants(redactSet, resolved)

		if plan == nil {
			plan = &browserSecretPlan{
				ExtraEnv:    map[string]string{},
				EnvVarNames: map[string]bool{},
			}
		}
		envName := fmt.Sprintf("%s%d", browserSecretEnvPrefix, envIdx)
		envIdx++
		plan.ExtraEnv[envName] = resolved
		plan.EnvVarNames[envName] = true
		rewritten[i] = "$" + envName
	}

	if plan == nil {
		return nil, nil
	}
	plan.RewrittenArgs = rewritten
	plan.RedactValues = sortedRedactValues(redactSet)
	return plan, nil
}

func collectRedactVariants(dst map[string]struct{}, value string) {
	if len(value) < 3 {
		return
	}
	dst[value] = struct{}{}
	if encoded, err := json.Marshal(value); err == nil && len(encoded) >= 2 {
		escaped := string(encoded[1 : len(encoded)-1])
		if len(escaped) >= 3 {
			dst[escaped] = struct{}{}
		}
	}
}

func sortedRedactValues(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

func redactBrowserText(text string, values []string) string {
	if text == "" || len(values) == 0 {
		return text
	}
	redacted := text
	for _, value := range values {
		if len(value) < 3 {
			continue
		}
		redacted = strings.ReplaceAll(redacted, value, "[REDACTED]")
	}
	return redacted
}

func redactBrowserError(err error, values []string) error {
	if err == nil || len(values) == 0 {
		return err
	}
	msg := err.Error()
	redacted := redactBrowserText(msg, values)
	if redacted == msg {
		return err
	}
	return fmt.Errorf("%s", redacted)
}

func applyBrowserSecretPlan(opts *ExecuteOptions, plan *browserSecretPlan) *ExecuteOptions {
	if plan == nil {
		return opts
	}
	cloned := &ExecuteOptions{}
	if opts != nil {
		*cloned = *opts
		if len(opts.ExtraEnv) > 0 {
			cloned.ExtraEnv = make(map[string]string, len(opts.ExtraEnv)+len(plan.ExtraEnv))
			for k, v := range opts.ExtraEnv {
				cloned.ExtraEnv[k] = v
			}
		}
		if len(opts.ShellExpandEnvVars) > 0 {
			cloned.ShellExpandEnvVars = make(map[string]bool, len(opts.ShellExpandEnvVars)+len(plan.EnvVarNames))
			for k, v := range opts.ShellExpandEnvVars {
				cloned.ShellExpandEnvVars[k] = v
			}
		}
		if len(opts.RedactValues) > 0 {
			cloned.RedactValues = append([]string(nil), opts.RedactValues...)
		}
	}
	if cloned.ExtraEnv == nil {
		cloned.ExtraEnv = make(map[string]string, len(plan.ExtraEnv))
	}
	for k, v := range plan.ExtraEnv {
		cloned.ExtraEnv[k] = v
	}
	if cloned.ShellExpandEnvVars == nil {
		cloned.ShellExpandEnvVars = make(map[string]bool, len(plan.EnvVarNames))
	}
	for k, v := range plan.EnvVarNames {
		cloned.ShellExpandEnvVars[k] = v
	}
	if len(plan.RedactValues) > 0 {
		set := make(map[string]struct{}, len(cloned.RedactValues)+len(plan.RedactValues))
		for _, v := range cloned.RedactValues {
			set[v] = struct{}{}
		}
		for _, v := range plan.RedactValues {
			set[v] = struct{}{}
		}
		cloned.RedactValues = sortedRedactValues(set)
	}
	return cloned
}

// sessionSecretsForBrowser returns this turn's own SECRET_* values, merging
// selected Vault keys (Env) with any per-call browser-only secrets bound to
// the calling session (BrowserSecrets). Existing session access boundaries are
// preserved (Run/Ask turns receive none, and BrowserSecrets are never inherited
// from another session).
func sessionSecretsForBrowser(agentSessionID, workflowSessionID string) map[string]string {
	extractEnv := func(cfg *common.SessionShellConfig) map[string]string {
		if cfg == nil || len(cfg.Env) == 0 {
			return nil
		}
		out := map[string]string{}
		for k, v := range cfg.Env {
			if strings.HasPrefix(k, "SECRET_") && v != "" {
				out[k] = v
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}

	var agentCfg, workflowCfg *common.SessionShellConfig
	if agentSessionID != "" {
		agentCfg = common.GetSessionShellConfig(agentSessionID)
		if agentCfg != nil && agentCfg.CrewAskReadOnly {
			return nil
		}
	}
	if workflowSessionID != "" && workflowSessionID != agentSessionID {
		workflowCfg = common.GetSessionShellConfig(workflowSessionID)
		if workflowCfg != nil && workflowCfg.CrewAskReadOnly {
			return nil
		}
	}

	out := extractEnv(agentCfg)
	if len(out) == 0 {
		out = extractEnv(workflowCfg)
	}

	browserCfg := agentCfg
	if browserCfg == nil && agentSessionID == "" {
		browserCfg = workflowCfg
	}
	if browserCfg != nil && len(browserCfg.BrowserSecrets) > 0 {
		if out == nil {
			out = make(map[string]string, len(browserCfg.BrowserSecrets))
		}
		for k, v := range browserCfg.BrowserSecrets {
			if strings.HasPrefix(k, "SECRET_") && v != "" {
				out[k] = v
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func secretShellEnvRef(arg string, opts *ExecuteOptions) (string, bool) {
	if opts == nil || len(opts.ShellExpandEnvVars) == 0 || !strings.HasPrefix(arg, "$") {
		return "", false
	}
	name := arg[1:]
	if name == "" || !opts.ShellExpandEnvVars[name] {
		return "", false
	}
	return name, true
}
