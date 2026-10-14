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

	classifier := inspect(`raise RuntimeError("Never execute classifier source during inspection")
from dbos import DBOS
from agentworks import classifier as choose
import agentworks as platform
LABELS = {"billing": "Payments", "other": "Other requests"}
MODEL = "clef-flash"
@DBOS.step(name="choose_team")
async def classify(message):
    return await choose(state=message, categories=LABELS, model=MODEL, instructions="Choose the team")
@DBOS.step()
async def classify_again(message):
    return await platform.classifier(state=message, categories=LABELS)
@DBOS.workflow()
async def run(INPUT):
    decision = await classify(INPUT["message"])
    return await classify_again(decision)
`)
	encoded, _ = json.Marshal(classifier)
	if len(classifier["errors"].([]interface{})) != 0 || strings.Count(string(encoded), `"type":"classifier"`) != 2 {
		t.Fatalf("classifier aliases not recognized: %s", encoded)
	}
	for _, want := range []string{`"categories":{"billing":"Payments","other":"Other requests"}`, `"model":"clef-flash"`, `"instructions":"Choose the team"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("classifier details missing %s: %s", want, encoded)
		}
	}
}
