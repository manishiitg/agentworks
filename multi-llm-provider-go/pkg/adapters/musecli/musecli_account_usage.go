package musecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// ReadAccountUsage asks `muse usage --json` for the selected account's plan
// allowance. It runs no model turn and never changes the login. An API-key
// account has no allowance and returns no windows.
func ReadAccountUsage(ctx context.Context, environment []string) (llmtypes.AccountUsage, error) {
	result := llmtypes.AccountUsage{CheckedAt: time.Now().UTC()}
	binary, err := exec.LookPath("muse")
	if err != nil {
		return result, llmtypes.AccountUsageFailure("cli_unavailable")
	}
	dir, err := os.MkdirTemp("", "muse-account-usage-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "usage", "--json")
	cmd.Dir = dir
	cmd.Env = append([]string{}, environment...)
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return result, llmtypes.AccountUsageFailure("timeout")
	}
	if err != nil && len(output) == 0 {
		return result, llmtypes.AccountUsageFailure("probe_failed")
	}
	return parseMuseAccountUsage(output, result)
}

func parseMuseAccountUsage(raw []byte, result llmtypes.AccountUsage) (llmtypes.AccountUsage, error) {
	type window struct {
		UsedPercent *float64 `json:"usedPercent"`
		ResetsAtMs  int64    `json:"resetsAtMs"`
	}
	var response struct {
		Allowance *struct {
			Window *window `json:"window"`
			Weekly *window `json:"weekly"`
			AsOfMs int64   `json:"asOfMs"`
		} `json:"allowance"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return result, llmtypes.AccountUsageFailure("unreadable")
	}
	if response.Allowance == nil {
		return result, nil
	}
	if response.Allowance.AsOfMs > 0 {
		result.CheckedAt = time.UnixMilli(response.Allowance.AsOfMs).UTC()
	}
	for _, w := range []struct {
		label string
		value *window
	}{{"Window", response.Allowance.Window}, {"Weekly", response.Allowance.Weekly}} {
		if w.value == nil {
			continue
		}
		var reset time.Time
		if w.value.ResetsAtMs > 0 {
			reset = time.UnixMilli(w.value.ResetsAtMs)
		}
		result.AddWindow(w.label, w.value.UsedPercent, reset)
	}
	return result, nil
}
