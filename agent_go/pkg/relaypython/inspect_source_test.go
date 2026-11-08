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

// PLAT-746: the Script outline draws control flow. Blocks that hold steps or
// agent() calls become container rows with their steps nested; a block with
// none stays one "Other code" row.
func TestAutomationOutlineDrawsControlFlow(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	source := `from agentworks import agent, step

@step("Load account")
def load_account(inputs):
    return {"mode": inputs["mode"]}

@step("Bill company")
def bill_company(account):
    return account

@step("Bill person")
def bill_person(account):
    return account

@step("Send receipt")
def send_receipt(account):
    return account

@step("Notify team")
def notify(account):
    return send_receipt(account)

def main():
    account = load_account(inputs)
    mode = account["mode"]
    # Corporate accounts
    if mode == "corporate":
        bill_company(account)
        notify(account)
    # Personal accounts
    elif mode == "personal":
        bill_person(account)
        notify(account)
    else:
        send_receipt(account)
    for lead in leads:
        agent("Writer", "Draft a note")
    try:
        send_receipt(account)
    except Exception:
        agent("Helper", "Tell the team")
    with open("log.txt") as handle:
        print(handle.read())
`
	raw, _ := json.Marshal(map[string]string{"main.py": source})
	cmd := exec.Command("python3", "-I", "-c", SourceInspector)
	cmd.Env = append(os.Environ(), "VAR_AUTOMATION_CHECK_FILES="+base64.StdEncoding.EncodeToString(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspector: %s %v", out, err)
	}
	var graph struct {
		Nodes []struct {
			ID, Label, Role, Parent string
		} `json:"nodes"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(out, &graph); err != nil || len(graph.Errors) != 0 {
		t.Fatalf("outline: %s %v", out, err)
	}
	labels := map[string]string{}
	var rows []string
	for _, node := range graph.Nodes {
		labels[node.ID] = node.Label
		row := node.Label
		if node.Role != "" {
			row += " [" + node.Role + "]"
		}
		if node.Parent != "" {
			row += " in " + labels[node.Parent]
		}
		rows = append(rows, row)
	}
	want := []string{
		"Load account",
		"Choose a path [branch]",
		"Corporate accounts [case] in Choose a path",
		"Bill company in Corporate accounts",
		"Notify team in Corporate accounts",
		"Send receipt in Corporate accounts",
		"Personal accounts [case] in Choose a path",
		"Bill person in Personal accounts",
		"Notify team in Personal accounts",
		"Send receipt in Personal accounts",
		"Otherwise [case] in Choose a path",
		"Send receipt in Otherwise",
		"Repeat for each lead [repeat]",
		"Writer in Repeat for each lead",
		"Try [try]",
		"Send receipt in Try",
		"If this fails [failure] in Try",
		"Helper in If this fails",
		"Other code",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("outline rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}
