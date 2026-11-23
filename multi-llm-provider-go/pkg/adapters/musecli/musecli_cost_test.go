package musecli

import (
	"math"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Rate card pinned 2026-09-10 (Vercel AI Gateway changelog + VentureBeat +
// DataCamp): contributor 0.10/0.20/0.002, standard 1.25/4.25/0.15 per 1M.
func TestMuseModelRateCard(t *testing.T) {
	cases := []struct {
		model                 string
		input, output, cached float64
	}{
		{"muse-spark-1.3-contributor", 0.10, 0.20, 0.002},
		{"muse-spark-1.3", 1.25, 4.25, 0.15},
	}
	for _, c := range cases {
		meta, err := GetMuseModelMetadata(c.model)
		if err != nil || meta == nil {
			t.Fatalf("%s: metadata err=%v", c.model, err)
		}
		if meta.InputCostPer1MTokens != c.input || meta.OutputCostPer1MTokens != c.output || meta.CachedInputCostPer1MTokens != c.cached {
			t.Fatalf("%s: rates = %v/%v/%v, want %v/%v/%v", c.model,
				meta.InputCostPer1MTokens, meta.OutputCostPer1MTokens, meta.CachedInputCostPer1MTokens,
				c.input, c.output, c.cached)
		}
		if !meta.SupportsReasoningEffort || !meta.SupportsToolCalls {
			t.Fatalf("%s: want reasoning-effort + tool-call support advertised", c.model)
		}
	}
	if meta, _ := GetMuseModelMetadata("muse-spark-future"); meta.InputCostPer1MTokens != 0 {
		t.Fatal("unknown model must carry zero rates, not guesses")
	}
}

func TestMuseExecEffortValidation(t *testing.T) {
	// Tier ladder values must pass through.
	for _, effort := range []string{"xhigh", "high", "medium"} {
		opts := &llmtypes.CallOptions{}
		llmtypes.WithReasoningEffort(effort)(opts)
		got, err := museExecEffort(opts)
		if err != nil || got != effort {
			t.Fatalf("effort %q -> (%q, %v)", effort, got, err)
		}
	}
	if got, err := museExecEffort(nil); err != nil || got != "" {
		t.Fatalf("nil opts -> (%q, %v), want empty", got, err)
	}
	if got, err := museExecEffort(&llmtypes.CallOptions{}); err != nil || got != "" {
		t.Fatalf("unset effort -> (%q, %v), want empty (CLI default)", got, err)
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithReasoningEffort("turbo")(opts)
	if _, err := museExecEffort(opts); err == nil {
		t.Fatal("unknown effort must fail fast, not run at the wrong knob")
	}
}

// TestMuseAttachTurnCost replays the fixture usage shape (input includes the
// cache bucket, as proven live: warm-turn cached ~= prior-turn input) and
// pins the contributor-tier math.
func TestMuseAttachTurnCost(t *testing.T) {
	usage, ok := readMuseTranscriptUsage(writeMuseSampleLog(t), "run-r1")
	if !ok {
		t.Fatal("fixture usage missing")
	}
	gi := &llmtypes.GenerationInfo{}
	museAttachTurnCost(gi, "muse-spark-1.3-contributor", &usage)
	if gi.InputTokens == nil || *gi.InputTokens != 11 {
		t.Fatalf("input = %+v, want 11", gi.InputTokens)
	}
	if gi.CachedContentTokens == nil || *gi.CachedContentTokens != 3 {
		t.Fatalf("cached = %+v, want 3", gi.CachedContentTokens)
	}
	if marked, _ := gi.Additional["prompt_tokens_include_cache"].(bool); !marked {
		t.Fatal("cache present: input must be marked as including the cache bucket")
	}
	// (11-3)*0.10 + 7*0.20 + 3*0.002 per 1M = 2.206e-6.
	want := 2.206e-6
	got, _ := gi.Additional["cost_usd_estimated"].(float64)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
	if id, _ := gi.Additional["cost_model_id"].(string); id != "muse-spark-1.3-contributor" {
		t.Fatalf("cost model = %q", id)
	}
	// No cache bucket, no marker; unknown model, no estimate.
	gi2 := &llmtypes.GenerationInfo{}
	plain := llmtypes.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}
	museAttachTurnCost(gi2, "muse-spark-1.3-contributor", &plain)
	if _, marked := gi2.Additional["prompt_tokens_include_cache"]; marked {
		t.Fatal("no cache: marker must be absent")
	}
	gi3 := &llmtypes.GenerationInfo{}
	museAttachTurnCost(gi3, "muse-spark-future", &plain)
	if _, has := gi3.Additional["cost_usd_estimated"]; has {
		t.Fatal("zero-rate model must leave the estimate off, not invent one")
	}
	museAttachTurnCost(nil, "muse-spark-1.3-contributor", &plain)
	museAttachTurnCost(&llmtypes.GenerationInfo{}, "muse-spark-1.3-contributor", nil)
}
