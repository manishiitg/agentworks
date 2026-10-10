package relaypython

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeSourceOverviewUsesASTWithoutExecutingSource(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	source := `raise RuntimeError("Inspection must never execute this")
from dbos import DBOS as D
from agentworks import agent
@D.step(name="verify")
async def check(INPUT):
    return await agent(system_prompt="Read", user_message=str(INPUT))
@D.step()
async def review(INPUT):
    return INPUT
@D.workflow()
async def run(INPUT):
    result = await check(INPUT)
    if result["needs_review"]:
        result = await review(result)
    return result
`
	inspect := func(source string) map[string]interface{} {
		t.Helper()
		cmd := exec.Command("python3", "-I", "-c", SourceInspector)
		cmd.Env = append(os.Environ(), "VAR_RELAY_CHECK_SOURCE="+base64.StdEncoding.EncodeToString([]byte(source)))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("inspector: %s %v", out, err)
		}
		var graph map[string]interface{}
		if err := json.Unmarshal(out, &graph); err != nil {
			t.Fatal(err)
		}
		return graph
	}
	graph := inspect(source)
	if graph["native"] != true || len(graph["errors"].([]interface{})) != 0 || len(graph["nodes"].([]interface{})) != 5 {
		t.Fatalf("native branch graph: %v", graph)
	}
	encoded, _ := json.Marshal(graph)
	for _, want := range []string{`"label":"Yes"`, `"label":"No"`, `"call":"verify"`, `"type":"agent"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("missing %s: %s", want, encoded)
		}
	}
	if inspect("async def run(INPUT):\n    return INPUT\n")["validation_error"] == nil {
		t.Fatal("native entry accepted without DBOS decorator")
	}
	if inspect("async def run(INPUT, ctx):\n    return INPUT\n")["native"] != false {
		t.Fatal("existing Relay lost compatibility")
	}
	if inspect("async def run(INPUT, *args):\n    return INPUT\n")["validation_error"] == nil {
		t.Fatal("variadic entry accepted")
	}
}
