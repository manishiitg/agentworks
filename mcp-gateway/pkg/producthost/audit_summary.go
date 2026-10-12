package producthost

import (
	"cmp"
	"slices"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
)

// Summaries aggregate already persisted amounts, without provider pricing logic.
// Unfinished admissions remain visible in usage, but have no completed cost or
// latency observation. Query has already coalesced each request's two phases.
func summarizeAudit(events []api.AuditEvent) api.AuditSummary {
	out := api.AuditSummary{Total: len(events), ByDay: []api.UsageBucket{}, ByTool: []api.UsageBucket{}}
	days, tools := map[string]*api.UsageBucket{}, map[string]*api.UsageBucket{}
	users, models := map[string]*api.LLMCostBucket{}, map[string]*api.LLMCostBucket{}
	var duration, completed int64
	for _, e := range events {
		if e.Decision == api.DecisionAllow {
			out.Allowed++
		} else {
			out.Denied++
		}
		if e.Outcome == api.OutcomeUpstreamError {
			out.UpstreamErrors++
		}
		if e.Phase != "started" {
			duration += e.DurationMs
			completed++
		}
		for _, pair := range []struct {
			buckets map[string]*api.UsageBucket
			key     string
		}{
			{days, e.Timestamp.UTC().Format("2006-01-02")}, {tools, e.PublicName},
		} {
			bucket := pair.buckets[pair.key]
			if bucket == nil {
				bucket = &api.UsageBucket{Key: pair.key}
				pair.buckets[pair.key] = bucket
			}
			bucket.Count++
			if e.Decision != api.DecisionAllow {
				bucket.Denied++
			}
			if e.Outcome == api.OutcomeUpstreamError {
				bucket.UpstreamErrors++
			}
		}
		if e.Kind != "llm" || e.Phase == "started" {
			continue
		}
		if out.Costs == nil {
			out.Costs = &api.LLMCostSummary{ByUser: []api.LLMCostBucket{}, ByModel: []api.LLMCostBucket{}}
		}
		addCost(&out.Costs.LLMCostBucket, e)
		for _, pair := range []struct {
			buckets map[string]*api.LLMCostBucket
			key     string
		}{
			{users, e.UserID}, {models, e.PublicName},
		} {
			bucket := pair.buckets[pair.key]
			if bucket == nil {
				bucket = &api.LLMCostBucket{Key: pair.key}
				pair.buckets[pair.key] = bucket
			}
			addCost(bucket, e)
		}
	}
	if completed > 0 {
		out.AvgDurationMs = duration / completed
	}
	for _, b := range days {
		out.ByDay = append(out.ByDay, *b)
	}
	for _, b := range tools {
		out.ByTool = append(out.ByTool, *b)
	}
	slices.SortFunc(out.ByDay, func(a, b api.UsageBucket) int { return cmp.Compare(b.Key, a.Key) })
	slices.SortFunc(out.ByTool, func(a, b api.UsageBucket) int {
		if count := cmp.Compare(b.Count, a.Count); count != 0 {
			return count
		}
		return cmp.Compare(a.Key, b.Key)
	})
	if out.Costs != nil {
		for _, b := range users {
			out.Costs.ByUser = append(out.Costs.ByUser, *b)
		}
		for _, b := range models {
			out.Costs.ByModel = append(out.Costs.ByModel, *b)
		}
		slices.SortFunc(out.Costs.ByUser, func(a, b api.LLMCostBucket) int { return cmp.Compare(a.Key, b.Key) })
		slices.SortFunc(out.Costs.ByModel, func(a, b api.LLMCostBucket) int { return cmp.Compare(a.Key, b.Key) })
	}
	return out
}

func addCost(bucket *api.LLMCostBucket, event api.AuditEvent) {
	bucket.Requests++
	cost := event.LLMCost
	if cost == nil || cost.Status == "unknown" {
		bucket.UnknownRequests++
		return
	}
	if cost.Status == "not_billable" {
		bucket.UnbilledRequests++
		return
	}
	if cost.Status != "estimated" && cost.Status != "interrupted_estimate" {
		bucket.UnknownRequests++
		return
	}
	bucket.PricedRequests++
	bucket.AmountNanoUSD += cost.AmountNanoUSD
	bucket.InputTokens += cost.Usage.Input + cost.Usage.CacheRead + cost.Usage.CacheWrite5m + cost.Usage.CacheWrite1h
	bucket.OutputTokens += cost.Usage.Output
	if cost.Status == "interrupted_estimate" {
		bucket.InterruptedRequests++
		bucket.InterruptedAmountNanoUSD += cost.AmountNanoUSD
	}
}
