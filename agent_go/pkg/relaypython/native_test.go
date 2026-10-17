package relaypython

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBOSNativeClassifierReceiptSurvivesCheckpointRecovery(t *testing.T) {
	c, cfg := dbosFixture(t, `import io, json, os, urllib.request
from dbos import DBOS
from agentworks import classifier, agent, run_dir

def inference(request, timeout):
    assert request.full_url.endswith("/ai/run/@cf/cloudflare/clef-flash")
    assert request.get_header("Authorization") == "Bearer fixture-selected-token"
    assert json.loads(request.data)["questions"]["category"]["criteria"] == {"billing": "Payments", "other": "Other"}
    counter = run_dir / "inference-count.txt"
    counter.write_text(str(int(counter.read_text() if counter.exists() else "0") + 1))
    return io.BytesIO(json.dumps({"success": True, "result": {"model": "clef-flash", "answers": {"category": {
        "choice": "billing", "confidence": 0.8, "probabilities": {"billing": 0.9, "other": 0.1}
    }}, "usage": {"input_tokens": 10, "output_tokens": 0}}}).encode())
urllib.request.urlopen = inference

@DBOS.step(name="choose_team")
async def choose_team(message):
    return await classifier(state=message, categories={"billing": "Payments", "other": "Other"})

@DBOS.step(name="reply")
async def reply(label):
    return await agent(user_message="Draft a reply for " + label)

@DBOS.workflow()
async def run(INPUT):
    result = await choose_team(INPUT["message"])
    await reply(result["label"])
    if os.environ.get("FAULT_AFTER_CHECKPOINT") == "1":
        os._exit(97)
    return result
`, map[string]interface{}{"message": "Refund the duplicate charge"})
	cfg.Env = map[string]string{"SECRET_CLOUDFLARE_AUTH_TOKEN": "fixture-selected-token", "SECRET_CLOUDFLARE_ACCOUNT_ID": "fixture-account", "FAULT_AFTER_CHECKPOINT": "1"}
	agentCalls := 0
	cfg.CallAgent = func(ctx context.Context, call Call, tool ToolCaller) (interface{}, error) {
		agentCalls++
		if len(call.Messages) != 1 || call.Messages[0] != "Draft a reply for billing" || call.SystemPrompt != "" {
			t.Fatalf("classifier result did not reach the agent: %+v", call)
		}
		return map[string]interface{}{"reply": "Billing reply"}, nil
	}
	if err := runDBOS(t, cfg); !IsInterrupted(err) {
		t.Fatalf("classifier checkpoint interruption: %v", err)
	}
	delete(cfg.Env, "FAULT_AFTER_CHECKPOINT")
	if err := runDBOS(t, cfg); err != nil {
		t.Fatal(err)
	}
	count, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "inference-count.txt"))
	if string(count) != "1" {
		t.Fatalf("completed classification repeated: %s", count)
	}
	if agentCalls != 1 {
		t.Fatalf("Classifier to Agent chain failed or repeated: %d agent calls", agentCalls)
	}
	raw, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "relay_trace.json"))
	var trace struct {
		Calls []struct {
			Type            string
			Provider        string
			Reused          bool              `json:"checkpoint_reused"`
			ClassifierCalls []json.RawMessage `json:"classifier_calls"`
			AgentCalls      []json.RawMessage `json:"agent_calls"`
		}
	}
	if err := json.Unmarshal(raw, &trace); err != nil || len(trace.Calls) != 2 || trace.Calls[0].Type != "classifier" || trace.Calls[0].Provider != "cloudflare" || !trace.Calls[0].Reused || len(trace.Calls[0].ClassifierCalls) != 1 || len(trace.Calls[0].AgentCalls) != 0 || len(trace.Calls[1].AgentCalls) != 1 {
		t.Fatalf("typed classifier history lost: %s (%v)", raw, err)
	}
	if strings.Contains(string(raw), "fixture-selected-token") || strings.Contains(string(raw), "fixture-account") {
		t.Fatal("classifier trace contains credential values")
	}
}

func TestDBOSNativeWorkflowHistoryRecoversAgentWithoutRelayAnnotations(t *testing.T) {
	c, cfg := dbosFixture(t, `import os
from dbos import DBOS
from agentworks import agent, tool, run_dir

@tool
async def lookup(order_id: str):
    return {"order_id": order_id, "code": "native-original"}

@DBOS.step(name="verify_order")
async def verify_order(order_id):
    DBOS.logger.info("Verifying native order")
    return await agent(name="lookup", system_prompt="Read the order", user_message=order_id, tools=[lookup])

@DBOS.step(name="review_order")
async def review_order(verified):
    return await agent(name="review", system_prompt="Review", user_message=str(verified))

@DBOS.workflow(name="process_order", max_recovery_attempts=3)
async def run(INPUT):
    verified = await verify_order(INPUT["order_id"])
    if os.environ.get("FAULT_AFTER_CHECKPOINT") == "1":
        os._exit(97)
    return await review_order(verified)
`, map[string]interface{}{"order_id": "native-order"})
	counts := map[string]int{}
	cfg.Env = map[string]string{"FAULT_AFTER_CHECKPOINT": "1"}
	cfg.CallAgent = func(ctx context.Context, call Call, tool ToolCaller) (interface{}, error) {
		counts[call.Name]++
		if call.Name == "lookup" {
			value, err := tool(ctx, "lookup", map[string]interface{}{"order_id": "native-order"})
			if err != nil || !strings.Contains(value, "native-original") {
				t.Fatalf("Python tool: %s %v", value, err)
			}
			return AgentResult{Output: map[string]interface{}{"order_id": "native-order", "code": "native-original"}, Provider: "fixture", Model: "fixture"}, nil
		}
		if !strings.Contains(call.Messages[0], "native-original") {
			t.Fatalf("checkpoint output lost: %+v", call)
		}
		return map[string]interface{}{"accepted": true}, nil
	}
	if err := runDBOS(t, cfg); !IsInterrupted(err) {
		t.Fatalf("native process interruption: %v", err)
	}
	delete(cfg.Env, "FAULT_AFTER_CHECKPOINT")
	if err := runDBOS(t, cfg); err != nil {
		t.Fatal(err)
	}
	if counts["lookup"] != 1 || counts["review"] != 1 {
		t.Fatalf("checkpoint repeated: %v", counts)
	}
	raw, err := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "relay_trace.json"))
	var trace struct {
		Model string `json:"execution_model"`
		Calls []struct {
			Name     string
			StepID   int  `json:"dbos_step_id"`
			Reused   bool `json:"checkpoint_reused"`
			Tools    []interface{}
			Provider string
		}
	}
	if err != nil || json.Unmarshal(raw, &trace) != nil || trace.Model != "native-dbos" || len(trace.Calls) != 2 || trace.Calls[0].Name != "verify_order" || !trace.Calls[0].Reused || len(trace.Calls[0].Tools) != 1 || trace.Calls[0].Provider != "fixture" {
		t.Fatalf("not actual DBOS history with linked receipts: %s %v", raw, err)
	}
	events, err := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "dbos_events.jsonl"))
	if err != nil || !strings.Contains(string(events), "Verifying native order") || !strings.Contains(string(events), `"step_id": 1`) {
		t.Fatalf("DBOS logs not linked to step: %s %v", events, err)
	}
}

func TestDBOSNativeUncertainAgentStopsWithoutRepeatingTool(t *testing.T) {
	c, cfg := dbosFixture(t, `import os
from dbos import DBOS
from agentworks import agent, tool, run_dir

@tool
async def send():
    file = run_dir / "effects.txt"
    file.write_text((file.read_text() if file.exists() else "") + "sent\n")
    os._exit(97)

@DBOS.step()
async def send_order():
    return await agent(name="send", system_prompt="Send", user_message="go", tools=[send])

@DBOS.workflow()
async def run(INPUT):
    return await send_order()
`, map[string]interface{}{})
	count := 0
	cfg.CallAgent = func(ctx context.Context, _ Call, tool ToolCaller) (interface{}, error) {
		count++
		_, err := tool(ctx, "send", map[string]interface{}{})
		return nil, err
	}
	if err := runDBOS(t, cfg); !IsInterrupted(err) {
		t.Fatalf("first native interruption: %v", err)
	}
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "requires reconciliation") {
		t.Fatalf("uncertain action admitted: %v", err)
	}
	if count != 1 {
		t.Fatalf("agent repeated %d times", count)
	}
	raw, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "effects.txt"))
	if string(raw) != "sent\n" {
		t.Fatalf("tool repeated: %s", raw)
	}
}

func TestDBOSNativeAutomaticRetryCannotRepeatUncertainAgent(t *testing.T) {
	_, cfg := dbosFixture(t, `from dbos import DBOS
from agentworks import agent

@DBOS.step(retries_allowed=True, max_attempts=2, interval_seconds=0.01)
async def send_order():
    return await agent(name="send", system_prompt="Send", user_message="go")

@DBOS.workflow()
async def run(INPUT):
    return await send_order()
`, map[string]interface{}{})
	count := 0
	cfg.CallAgent = func(context.Context, Call, ToolCaller) (interface{}, error) {
		count++
		return nil, errors.New("service result uncertain")
	}
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "requires reconciliation") {
		t.Fatalf("automatic retry bypassed uncertain intent: %v", err)
	}
	if count != 1 {
		t.Fatalf("uncertain agent repeated %d times", count)
	}
}

func TestDBOSNativeRestartAgentReusesCompletedToolResult(t *testing.T) {
	c, cfg := dbosFixture(t, `import asyncio, json, os
from dbos import DBOS
from agentworks import agent, tool, run_dir

@tool
async def send(order_id: str):
    with (run_dir / "effects.txt").open("a") as f:
        f.write(order_id + "\n")
    return {"receipt": "saved-receipt", "order_id": order_id}

async def crash_after_tool_saved():
    while True:
        if any(json.loads(p.read_text()).get("status") == "completed" for p in (run_dir / ".relay_dbos/tools-1-1").glob("*.json")):
            os._exit(97)
        await asyncio.sleep(0.01)

@DBOS.step()
async def send_order():
    if os.environ.get("FAULT") == "1":
        asyncio.create_task(crash_after_tool_saved())
    return await agent(name=None, recovery="restart", system_prompt="Send", user_message="go", tools=[send])

@DBOS.workflow()
async def run(INPUT):
    return await send_order()
`, map[string]interface{}{})
	count := 0
	cfg.Env = map[string]string{"FAULT": "1"}
	cfg.CallAgent = func(ctx context.Context, call Call, tool ToolCaller) (interface{}, error) {
		count++
		if call.Recovery != "restart" {
			t.Fatalf("missing recovery policy: %+v", call)
		}
		result, err := tool(ctx, "send", map[string]interface{}{"order_id": "order-1"})
		if err != nil {
			return nil, err
		}
		if count == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		// Repeated requests in the new session must also reuse the same result.
		again, err := tool(ctx, "send", map[string]interface{}{"order_id": "order-1"})
		if err != nil || result != again || !strings.Contains(result, "saved-receipt") {
			t.Fatalf("tool result was not reused: %q %q %v", result, again, err)
		}
		return AgentResult{Output: map[string]interface{}{"receipt": "saved-receipt"}, Provider: "fixture"}, nil
	}
	if err := runDBOS(t, cfg); !IsInterrupted(err) {
		t.Fatalf("expected process crash: %v", err)
	}
	delete(cfg.Env, "FAULT")
	if err := runDBOS(t, cfg); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected restarted agent, got %d calls", count)
	}
	effects, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "effects.txt"))
	if string(effects) != "order-1\n" {
		t.Fatalf("tool action repeated: %s", effects)
	}
}

func TestDBOSNativeRestartAgentResolvesInterruptedToolOrStops(t *testing.T) {
	for _, resolvable := range []bool{true, false} {
		t.Run(fmt.Sprint(resolvable), func(t *testing.T) {
			recovery := "@tool"
			if resolvable {
				recovery = "@tool(recover=recover_send)"
			}
			c, cfg := dbosFixture(t, `import json, os
from dbos import DBOS
from agentworks import agent, tool, run_dir

async def recover_send(order_id: str):
    return json.loads((run_dir / "service-receipt.json").read_text())

`+recovery+`
async def send(order_id: str):
    with (run_dir / "effects.txt").open("a") as f:
        f.write(order_id + "\n")
    (run_dir / "service-receipt.json").write_text(json.dumps({"receipt": "service-original"}))
    os._exit(97)

@DBOS.step()
async def send_order():
    return await agent(name="send", recovery="restart", system_prompt="Send", user_message="go", tools=[send])

@DBOS.workflow()
async def run(INPUT):
    return await send_order()
`, map[string]interface{}{})
			count := 0
			cfg.CallAgent = func(ctx context.Context, _ Call, tool ToolCaller) (interface{}, error) {
				count++
				result, err := tool(ctx, "send", map[string]interface{}{"order_id": "order-2"})
				return result, err
			}
			if err := runDBOS(t, cfg); !IsInterrupted(err) {
				t.Fatalf("expected process crash: %v", err)
			}
			err := runDBOS(t, cfg)
			if resolvable {
				if err != nil || count != 2 {
					t.Fatalf("could not recover service receipt: count=%d err=%v", count, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "requires reconciliation") || count != 1 {
				t.Fatalf("uncertain action was restarted: count=%d err=%v", count, err)
			}
			effects, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "effects.txt"))
			if string(effects) != "order-2\n" {
				t.Fatalf("tool action repeated: %s", effects)
			}
		})
	}
}

func TestDBOSNativeRestartRejectsUnjournaledCapabilities(t *testing.T) {
	_, cfg := dbosFixture(t, `from dbos import DBOS
from agentworks import agent

@DBOS.step()
async def act():
    return await agent(recovery="restart", system_prompt="Act", user_message="go", mcp=[{"server":"external"}])

@DBOS.workflow()
async def run(INPUT):
    return await act()
`, map[string]interface{}{})
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "journaled Python tools only") {
		t.Fatalf("unjournaled tools admitted: %v", err)
	}
}
