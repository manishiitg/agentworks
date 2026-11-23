package codexcli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// ReadAccountUsage initializes an isolated app-server solely for account quota.
// It never creates a thread, sends a prompt, or changes the selected login.
func ReadAccountUsage(ctx context.Context, environment []string) (llmtypes.AccountUsage, error) {
	result := llmtypes.AccountUsage{CheckedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "codex-account-usage-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	cmd.Dir = dir
	cmd.Env = append([]string{}, environment...)
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return result, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return result, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return result, llmtypes.AccountUsageFailure("spawn_failed")
	}
	r := &codexRPC{cmd: cmd, stdin: stdin, pending: make(map[string]chan codexRPCMessage), events: make(chan codexRPCMessage, 128), done: make(chan struct{}), exited: make(chan struct{}), stop: cancel}
	go r.read(stdout)
	go func() { err := cmd.Wait(); r.finish(err); close(r.exited) }()
	defer r.close()
	if err = r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "agentworks_usage", "version": "1"}}, nil); err != nil {
		return result, llmtypes.AccountUsageFailure("initialization_failed")
	}
	if err = r.write(map[string]any{"method": "initialized"}); err != nil {
		return result, llmtypes.AccountUsageFailure("initialization_failed")
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err = r.call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return result, llmtypes.AccountUsageFailure("account_read_failed")
	}
	// API-key and signed-out accounts have no ChatGPT subscription quota.
	if account.Account == nil || account.Account.Type == "apiKey" {
		return result, nil
	}
	var raw json.RawMessage
	if err = r.call(ctx, "account/rateLimits/read", map[string]any{}, &raw); err != nil {
		return result, llmtypes.AccountUsageFailure("quota_read_failed")
	}
	return parseCodexAccountUsage(raw, result)
}
func parseCodexAccountUsage(raw []byte, result llmtypes.AccountUsage) (llmtypes.AccountUsage, error) {
	type window struct {
		UsedPercent   *float64 `json:"usedPercent"`
		WindowMinutes int      `json:"windowDurationMins"`
		ResetsAt      int64    `json:"resetsAt"`
	}
	type limits struct {
		Primary   *window `json:"primary"`
		Secondary *window `json:"secondary"`
	}
	var response struct {
		Limits *limits            `json:"rateLimits"`
		ByID   map[string]*limits `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return result, llmtypes.AccountUsageFailure("unreadable")
	}
	appendLimits := func(prefix string, l *limits) {
		if l == nil {
			return
		}
		for i, w := range []*window{l.Primary, l.Secondary} {
			if w == nil {
				continue
			}
			fallback := "Primary limit"
			if i == 1 {
				fallback = "Secondary limit"
			}
			label := codexWindowLabel(w.WindowMinutes, fallback)
			var reset time.Time
			if w.ResetsAt > 0 {
				reset = time.Unix(w.ResetsAt, 0)
			}
			result.AddWindow(prefix+label, w.UsedPercent, reset)
		}
	}
	if len(response.ByID) > 0 {
		ids := make([]string, 0, len(response.ByID))
		for id := range response.ByID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			appendLimits(id+" · ", response.ByID[id])
		}
	} else {
		appendLimits("", response.Limits)
	}
	return result, nil
}
