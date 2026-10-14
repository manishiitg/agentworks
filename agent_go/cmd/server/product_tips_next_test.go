package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// The server alone picks the working tip: unseen first, then the one shown longest ago once its cooldown has passed,
// nothing while all are cooling down; features the browser says were used, and Code tab counts, are respected.
func TestSelectProductTipChoosesOnTheServer(t *testing.T) {
	one := 1
	tips := []agentprofiles.WorkingTip{
		{ID: "a", Text: "First", SkipIfUsed: "quick-switcher"},
		{ID: "b", Text: "Second"},
		{ID: "c", Text: "Only with one tab", When: &agentprofiles.TipConditions{CodeTabs: &agentprofiles.TipTabRange{Max: &one}}},
	}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	stamp := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	ctx := productTipContext{Surface: "code", CodeTabs: 1, Used: map[string]bool{}}

	if got := selectProductTip(tips, ctx, productTipsState{}, now); got == nil || got.ID != "a" {
		t.Fatalf("a new person gets the first unseen tip, got %+v", got)
	}
	ctx.Used["quick-switcher"] = true
	if got := selectProductTip(tips, ctx, productTipsState{}, now); got == nil || got.ID != "b" {
		t.Fatalf("a tip for a feature already used is skipped, got %+v", got)
	}
	ctx.CodeTabs = 3
	state := productTipsState{Shown: map[string]string{"b": stamp(time.Hour)}}
	if got := selectProductTip(tips, ctx, state, now); got != nil {
		t.Fatalf("a tip shown an hour ago is cooling down and the other is out of range, got %+v", got)
	}
	state.Shown["b"] = stamp(productTipCooldown + time.Hour)
	if got := selectProductTip(tips, ctx, state, now); got == nil || got.ID != "b" {
		t.Fatalf("a tip returns once its cooldown has passed, got %+v", got)
	}
}
