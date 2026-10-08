package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

type recordedTool struct {
	desc   string
	params map[string]interface{}
	exec   func(context.Context, map[string]interface{}) (string, error)
}

type recordingRegistrar struct {
	tools map[string]recordedTool
}

func (r *recordingRegistrar) RegisterCustomTool(name, desc string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ string) error {
	if r.tools == nil {
		r.tools = map[string]recordedTool{}
	}
	r.tools[name] = recordedTool{desc: desc, params: params, exec: exec}
	return nil
}

func (r *recordingRegistrar) RegisterCustomToolWithTimeout(name, desc string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return r.RegisterCustomTool(name, desc, params, exec, category)
}

func TestOpenWorkspaceViewToolOpensAKnownViewAndRefusesOthers(t *testing.T) {
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerOpenWorkspaceViewTool(reg, "s1", "Workflow/x"); err != nil {
		t.Fatal(err)
	}
	open, ok := reg.tools["perform_ui_action"]
	if !ok || len(reg.tools) != 3 {
		t.Fatalf("expected three UI tools, got %+v", reg.tools)
	}
	out, err := open.exec(context.Background(), map[string]interface{}{"view": "report", "action": "open"})
	if err != nil || !strings.Contains(out, `"code":"browser_disconnected"`) {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if out, err := open.exec(context.Background(), map[string]interface{}{"view": "dashboard", "action": "open"}); err != nil || !strings.Contains(out, "unsupported_view") {
		t.Fatalf("unknown view must reject: %s %v", out, err)
	}
	// database/learnings/knowledgebase consolidated into the knowledge view;
	// the old standalone view id must reject, the section target must route.
	if out, err := open.exec(context.Background(), map[string]interface{}{"view": "database", "action": "open"}); err != nil || !strings.Contains(out, "unsupported_view") {
		t.Fatalf("retired standalone view must reject: %s %v", out, err)
	}
	out, err = open.exec(context.Background(), map[string]interface{}{"view": "knowledge", "action": "refresh", "target": "database"})
	if err != nil || !strings.Contains(out, "browser_disconnected") {
		t.Fatalf("refresh out=%s err=%v", out, err)
	}
	targeted, err := open.exec(context.Background(), map[string]interface{}{"view": "flow", "action": "open", "target": "step-fetch"})
	if err != nil || !strings.Contains(targeted, `"code":"browser_disconnected"`) {
		t.Fatalf("targeted open = %s err=%v", targeted, err)
	}
	tv, err := workspaceViewAction("flow", "Workflow/x", "open", " step-fetch ")
	if err != nil || tv.Payload["target"] != "step-fetch" || tv.Activity.Detail != "Plan · step-fetch" {
		t.Fatalf("target must be trimmed and shown in the activity row: %+v err=%v", tv, err)
	}
	if untargeted, _ := workspaceViewAction("flow", "Workflow/x", "open", "  "); untargeted.Payload["target"] != nil {
		t.Fatalf("a blank target must not reach the payload: %+v", untargeted.Payload)
	}
	ev, err := workspaceViewPresentation("costs", "Workflow/x")
	if err != nil || ev.Kind != WorkflowViewPresentationKind || ev.Payload["view"] != "costs" || ev.Payload["action"] != "open" || ev.Activity == nil || ev.Activity.Detail != "Costs" || ev.WorkspacePath != "Workflow/x" {
		t.Fatalf("event = %+v err=%v", ev, err)
	}
	rv, err := workspaceViewAction("report", "Workflow/x", "refresh", "")
	if err != nil || rv.Payload["action"] != "refresh" || rv.Activity.Label != "Refresh requested" || rv.PresentationID == ev.PresentationID {
		t.Fatalf("refresh event = %+v err=%v", rv, err)
	}
}

func TestOpenWorkspaceViewWaitsForSelectedStepReceipt(t *testing.T) {
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerOpenWorkspaceViewTool(reg, "s1", "Workflow/x"); err != nil {
		t.Fatal(err)
	}
	b := api.uiBroker()
	c, err := b.bind("s1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() {
		out, _ := reg.tools["perform_ui_action"].exec(ctx, map[string]interface{}{"view": "flow", "action": "open", "target": "livekit-quality"})
		result <- out
	}()
	var commands []uiAction
	for len(commands) == 0 && ctx.Err() == nil {
		commands, err = b.syncClient("s1", c.id, c.token, uiSnapshot{View: "costs", Revision: 1, Visible: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(commands) == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	if len(commands) != 1 {
		t.Fatal("no browser action queued")
	}
	select {
	case out := <-result:
		t.Fatalf("returned before receipt: %s", out)
	default:
	}
	if err := b.ack("s1", c.id, c.token, commands[0].RequestID, "applied", "", uiSnapshot{View: "flow", Target: "livekit-quality", Revision: 2, Visible: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-result:
		if !strings.Contains(out, `"status":"applied"`) || !strings.Contains(out, `"visible":true`) {
			t.Fatal(out)
		}
	case <-ctx.Done():
		t.Fatal("receipt did not reach legacy caller")
	}
}
