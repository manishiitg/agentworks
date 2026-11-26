package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// No prompt is yielded: initialization and get_usage are control requests only.
const claudeAccountUsageScript = `
const {pathToFileURL} = require('node:url');
(async () => {
 const {query} = await import(pathToFileURL(process.argv[1]).href);
 const abort = new AbortController();
 const q = query({prompt:(async function*(){await new Promise(resolve => abort.signal.addEventListener('abort', resolve, {once:true}));})(),options:{
  abortController:abort, pathToClaudeCodeExecutable:process.argv[2], cwd:process.cwd(),
  persistSession:false, settingSources:[], settings:{disableAllHooks:true}, tools:[], mcpServers:{}, strictMcpConfig:true,
  env:{...process.env,ENABLE_CLAUDEAI_MCP_SERVERS:'false',CLAUDE_CODE_AUTO_CONNECT_IDE:'0',CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL:'1'},stderr:()=>{}
 }});
 try {
  await q.initializationResult();
  if (typeof q.usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET !== "function") {process.stdout.write(JSON.stringify({error:"method_unavailable"}));return;}
  const u = await q.usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET({skipBehaviors:true});
  process.stdout.write(JSON.stringify({rate_limits_available:u.rate_limits_available,rate_limits:u.rate_limits}));
 } finally {abort.abort();q.close();}
})().catch(()=>{process.exitCode=1});
`

// ReadAccountUsage reads only the explicitly supplied account environment.
func ReadAccountUsage(ctx context.Context, environment []string) (llmtypes.AccountUsage, error) {
	result := llmtypes.AccountUsage{CheckedAt: time.Now().UTC()}
	entry, err := claudeSDKEntry()
	if err != nil {
		return result, llmtypes.AccountUsageFailure("sdk_unavailable")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		return result, llmtypes.AccountUsageFailure("cli_unavailable")
	}
	dir, err := os.MkdirTemp("", "claude-account-usage-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "-e", claudeAccountUsageScript, entry, binary)
	cmd.Dir = dir
	cmd.Env = append([]string{}, environment...)
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return result, llmtypes.AccountUsageFailure("timeout")
	}
	if err != nil {
		return result, llmtypes.AccountUsageFailure("probe_failed")
	}
	return parseClaudeAccountUsage(output, result)
}
func parseClaudeAccountUsage(raw []byte, result llmtypes.AccountUsage) (llmtypes.AccountUsage, error) {
	type window struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var response struct {
		Error     string                     `json:"error"`
		Available bool                       `json:"rate_limits_available"`
		Limits    map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return result, llmtypes.AccountUsageFailure("unreadable")
	}
	if response.Error == "method_unavailable" {
		return result, llmtypes.AccountUsageFailure("method_unavailable")
	}
	if !response.Available {
		return result, nil
	}
	for _, item := range []struct{ key, label string }{{"five_hour", "5-hour limit"}, {"seven_day", "Weekly limit"}, {"seven_day_opus", "Weekly Opus limit"}, {"seven_day_sonnet", "Weekly Sonnet limit"}} {
		var w window
		if json.Unmarshal(response.Limits[item.key], &w) != nil {
			continue
		}
		reset, _ := time.Parse(time.RFC3339, w.ResetsAt)
		result.AddWindow(item.label, w.Utilization, reset)
	}
	var scoped []struct {
		window
		DisplayName string `json:"display_name"`
	}
	if json.Unmarshal(response.Limits["model_scoped"], &scoped) == nil {
		for _, w := range scoped {
			if w.DisplayName != "" {
				reset, _ := time.Parse(time.RFC3339, w.ResetsAt)
				result.AddWindow("Weekly · "+w.DisplayName, w.Utilization, reset)
			}
		}
	}
	return result, nil
}
