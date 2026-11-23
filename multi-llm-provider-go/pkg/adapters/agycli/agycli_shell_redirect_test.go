package agycli

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

func agyRunHook(t *testing.T, python, mode string, payload map[string]interface{}, hostPorts ...string) (decision, reason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", agyToolModeHookCommand(python, mode, hostPorts...))
	cmd.Stdin = strings.NewReader(string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook command: %v: %s", err, out)
	}
	var got struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("hook JSON: %v: %s", err, out)
	}
	return got.Decision, got.Reason
}

func TestAgyShellRedirectHook(t *testing.T) {
	t.Setenv(nativeshell.EnvVar, "on") // the redirect only matters while the native shell is on
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	shell := func(command string) map[string]interface{} {
		// The real AGY hook payload (captured live, 1.2.16): toolCall.args.CommandLine is the plain command.
		return map[string]interface{}{"toolCall": map[string]interface{}{
			"name": "run_command", "args": map[string]string{"CommandLine": command, "Cwd": "/tmp"},
		}}
	}
	for _, command := range []string{
		`curl -s "$MCP_CUSTOM/get_contract_upgrades" -H "$MCP_AUTH" --json '{}'`,
		`curl -s ${MCP_API_TOKEN}`,
		`curl -s http://127.0.0.1:1/tools/custom/x`,
		`curl http://h/tools/virtual/y`,
		`curl http://h/tools/mcp/z`,
	} {
		decision, reason := agyRunHook(t, python, "full", shell(command))
		if decision != "deny" {
			t.Fatalf("%q: decision = %q, want deny", command, decision)
		}
		for _, want := range []string{"missing or invalid Authorization header", "execute_shell_command", "call_mcp_tool"} {
			if !strings.Contains(reason, want) {
				t.Fatalf("%q: reason %q lacks %q", command, reason, want)
			}
		}
	}
	// Credential probing and the platform host:port are refused too.
	for _, command := range []string{
		`for v in MCP_AUTH MCP_CUSTOM; do [ -n "${!v}" ] && echo "$v=set" || echo "$v=unset"; done`,
		`env | grep MCP_AUTH`,
		`printenv MCP_CUSTOM`,
		`python3 -c "import os; print(os.environ['MCP_API_TOKEN'])"`,
		`test -n MCP_MCP; compgen -v | grep MCP_MCP`,
		`cat /proc/self/environ | grep MCP_AUTH`,
	} {
		decision, reason := agyRunHook(t, python, "full", shell(command))
		if decision != "deny" || !strings.Contains(reason, "execute_shell_command") || !strings.Contains(reason, "does NOT mean the bridge is missing") {
			t.Fatalf("probe %q: decision = %q reason = %q", command, decision, reason)
		}
	}
	for _, command := range []string{`curl -s http://127.0.0.1:18843/api/x`, `wget http://localhost:9/y`} {
		if decision, _ := agyRunHook(t, python, "full", shell(command), "127.0.0.1:18843", "localhost:9"); decision != "deny" {
			t.Fatalf("host:port %q: decision = %q, want deny", command, decision)
		}
		if decision, _ := agyRunHook(t, python, "full", shell(command)); decision != "allow" {
			t.Fatalf("without hosts %q: decision = %q, want allow", command, decision)
		}
	}
	for _, command := range []string{`echo hello`, `ls -la`, `ls`, `echo $MCP_CUSTOMER`, `git status`,
		`grep -rn MCP_AUTH code/`, `cat code/x/agentworks_db.py`, `python3 code/x/main.py`} {
		if decision, _ := agyRunHook(t, python, "full", shell(command)); decision != "allow" {
			t.Fatalf("%q: decision = %q, want allow", command, decision)
		}
	}
	// Only the native shell is redirected; other tools and the bridge pass.
	other := map[string]interface{}{"toolCall": map[string]interface{}{"name": "call_mcp_tool", "args": map[string]string{"Arguments": `{"command":"curl $MCP_CUSTOM/x"}`}}}
	if decision, _ := agyRunHook(t, python, "full", other); decision != "allow" {
		t.Fatalf("bridge call decision = %q, want allow", decision)
	}
	// mcp_only already denies every native tool; the redirect never grants.
	if decision, _ := agyRunHook(t, python, "mcp_only", shell(`echo hello`)); decision != "deny" {
		t.Fatalf("mcp_only native shell decision = %q, want deny", decision)
	}
	// Its own web search is the one native tool mcp_only keeps (PLAT-508 removed the platform's).
	search := map[string]interface{}{"toolCall": map[string]interface{}{"name": "search_web", "args": map[string]string{"query": "agentworks"}}}
	if decision, _ := agyRunHook(t, python, "mcp_only", search); decision != "allow" {
		t.Fatalf("mcp_only search_web decision = %q, want allow", decision)
	}
	view := map[string]interface{}{"toolCall": map[string]interface{}{"name": "view_file", "args": map[string]string{"path": "x"}}}
	if decision, _ := agyRunHook(t, python, "mcp_only", view); decision != "deny" {
		t.Fatalf("mcp_only view_file decision = %q, want deny", decision)
	}
}

func TestAgyShellRedirectKeepsManagedHookRestore(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	// The redirect lives inside the managed gate, so ownership detection (used for
	// stale-backup recovery and release) still recognises the entry in both modes.
	for _, mode := range []string{"mcp_only", "full"} {
		if !agyIsManagedToolHook(agyManagedToolHookEntry(python, mode)) {
			t.Fatalf("mode %s: managed hook entry not recognised", mode)
		}
	}
}
