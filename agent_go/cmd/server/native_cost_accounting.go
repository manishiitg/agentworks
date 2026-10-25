package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"
)

type nativeCostState struct {
	Current           costledger.Entry            `json:"current"`
	Provider          string                      `json:"provider"`
	NativeID          string                      `json:"native_id"`
	WorkingDir        string                      `json:"working_dir"`
	Path              string                      `json:"path"`
	Floor             time.Time                   `json:"floor"`
	PendingForeground bool                        `json:"pending_foreground"`
	Contexts          map[string]costledger.Entry `json:"source_contexts"`
}
type nativeCostAccountant struct {
	mu      sync.Mutex
	api     *StreamingAPI
	state   nativeCostState
	file    string
	done    chan struct{}
	gap     string
	usage   nativeusage.PrivateUsage
	stamp   string
	retired bool
}

func (api *StreamingAPI) initNativeCostAccounting() {
	costobserver.SetNativeAccountingFactory(func(base costledger.Entry, provider, model string) costobserver.NativeAccounting {
		return api.nativeCostAccounting(base, provider)
	})
	root, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(root, "native-cost", "*.json"))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var s nativeCostState
		if json.Unmarshal(raw, &s) != nil || s.Current.SessionID == "" || s.Current.UserID == "" || !nativeusage.Supported(s.Provider) {
			continue
		}
		if s.Contexts == nil {
			s.Contexts = map[string]costledger.Entry{}
		}
		a := &nativeCostAccountant{api: api, state: s, file: file, done: make(chan struct{})}
		api.nativeCostsMu.Lock()
		if api.nativeCosts == nil {
			api.nativeCosts = map[string]*nativeCostAccountant{}
		}
		api.nativeCosts[s.Current.SessionID] = a
		api.nativeCostsMu.Unlock()
		go a.run()
		log.Printf("[NATIVE_COST] restored session=%s provider=%s native=%s", s.Current.SessionID, s.Provider, s.NativeID)
	}
}

func (api *StreamingAPI) nativeCostAccounting(base costledger.Entry, provider string) costobserver.NativeAccounting {
	if base.SessionID == "" || base.UserID == "" || api.costLedger == nil {
		return nil
	}
	api.nativeCostsMu.Lock()
	defer api.nativeCostsMu.Unlock()
	if api.nativeCosts == nil {
		api.nativeCosts = map[string]*nativeCostAccountant{}
	}
	a := api.nativeCosts[base.SessionID]
	if a != nil {
		a.mu.Lock()
		changed := a.state.Provider != provider || a.state.Current.AccountID != base.AccountID
		if changed {
			a.retired = true
			close(a.done)
		}
		a.mu.Unlock()
		if changed {
			log.Printf("[NATIVE_COST] runtime_reset session=%s provider=%s reason=provider_or_account_changed", base.SessionID, provider)
			a = nil
		}
	}
	if a == nil {
		root, err := workflowCLIStateRoot()
		if err != nil {
			return nil
		}
		hash := sha256.Sum256([]byte(base.SessionID))
		a = &nativeCostAccountant{api: api, state: nativeCostState{Current: base, Provider: provider, Floor: base.Timestamp, Contexts: map[string]costledger.Entry{}}, file: filepath.Join(root, "native-cost", fmt.Sprintf("%x.json", hash[:20])), done: make(chan struct{})}
		api.nativeCosts[base.SessionID] = a
		go a.run()
	}
	a.Begin(base)
	return a
}

func (api *StreamingAPI) stopNativeCostAccounting() {
	costobserver.SetNativeAccountingFactory(nil)
	api.nativeCostsMu.Lock()
	defer api.nativeCostsMu.Unlock()
	for _, a := range api.nativeCosts {
		close(a.done)
	}
}
func (a *nativeCostAccountant) save() {
	if err := os.MkdirAll(filepath.Dir(a.file), 0700); err != nil {
		a.reportGap("checkpoint_directory")
		return
	}
	raw, err := json.Marshal(a.state)
	if err != nil {
		return
	}
	tmp := a.file + ".tmp"
	if err = os.WriteFile(tmp, raw, 0600); err == nil {
		err = os.Rename(tmp, a.file)
	}
	if err != nil {
		a.reportGap("checkpoint_write")
	}
}
func (a *nativeCostAccountant) reportGap(reason string) {
	if a.gap != reason {
		log.Printf("[NATIVE_COST] gap session=%s provider=%s reason=%s", a.state.Current.SessionID, a.state.Provider, reason)
		a.gap = reason
	}
}
func (a *nativeCostAccountant) Begin(base costledger.Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.retired {
		return
	}
	base.Provider = a.state.Provider
	if base.ModelID == "" {
		base.ModelID = a.state.Current.ModelID
	}
	a.state.Current = base
	if a.state.Contexts == nil {
		a.state.Contexts = map[string]costledger.Entry{}
	}
	a.save()
}
func (a *nativeCostAccountant) runtimeKey() string {
	id := a.state.NativeID
	if id == "" {
		id = "pending:" + a.state.Current.SessionID
	}
	return a.state.Provider + "\x1f" + a.state.Current.AccountID + "\x1f" + id
}

func (a *nativeCostAccountant) bind() error {
	if session, ok := mcpagent.LookupSession(a.state.Current.SessionID); ok {
		if h := session.Snapshot(); h != nil && h.Provider.NativeSessionID != "" {
			if string(h.Provider.Provider) != a.state.Provider {
				return fmt.Errorf("provider changed")
			}
			a.bindNativeID(h.Provider.NativeSessionID)
			a.state.WorkingDir = h.Provider.WorkingDir
			o := nativeTerminalObserver{api: a.api, provider: a.state.Provider, nativeID: a.state.NativeID, workingDir: a.state.WorkingDir}
			a.state.Path = o.nativeRecordPath()
		}
	}
	return nil
}

// bindNativeID is called with a.mu held. A new CLI runtime cannot inherit the
// old runtime's usage windows, source ownership or scan cursor.
func (a *nativeCostAccountant) bindNativeID(id string) {
	if a.state.NativeID != "" && a.state.NativeID != id {
		a.state.Contexts = map[string]costledger.Entry{}
		a.state.Floor = a.state.Current.Timestamp
		a.state.Path, a.state.WorkingDir = "", ""
		a.state.PendingForeground = false
		a.usage = nativeusage.PrivateUsage{}
		a.stamp = ""
		a.gap = ""
		log.Printf("[NATIVE_COST] runtime_reset session=%s provider=%s reason=native_session_changed", a.state.Current.SessionID, a.state.Provider)
	}
	a.state.NativeID = id
}

func (a *nativeCostAccountant) read() (nativeusage.Snapshot, error) {
	if err := a.bind(); err != nil {
		return nativeusage.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := nativeusage.Read(ctx, a.state.Provider, a.state.NativeID, a.state.Path)
	if err == nil {
		a.usage = s.Private
	}
	return s, err
}
func (a *nativeCostAccountant) contextFor(r nativeusage.Record) costledger.Entry {
	if r.TurnID == "" && r.Path == a.state.Path {
		return a.state.Current
	}
	key := r.Path + "\x1f" + r.TurnID
	if old, ok := a.state.Contexts[key]; ok {
		return old
	}
	base := a.state.Current
	if r.Path != a.state.Path {
		if old, ok := a.state.Contexts[r.Path]; ok {
			base = old
		} else {
			a.state.Contexts[r.Path] = base
		}
	}
	if r.ParentTurnID != "" {
		if old, ok := a.state.Contexts[a.state.Path+"\x1f"+r.ParentTurnID]; ok {
			base = old
		}
	}
	a.state.Contexts[key] = base
	return base
}
func (a *nativeCostAccountant) entry(r nativeusage.Record, c costledger.NativeCoverage) costledger.Entry {
	base := a.contextFor(r)
	if r.Model == "" && r.ParentModel == "" && r.OwnerType != "" && r.OwnerType != "main_root" {
		r.ParentModel = base.ModelID
	}
	e := costobserver.NativeEntry(base, a.state.Provider, r)
	if old, ok := c.Fallback[e.IdempotencyKey]; ok {
		r.Input = max(0, r.Input-int64(old.PromptTokens))
		r.Output = max(0, r.Output-int64(old.CompletionTokens))
		r.CacheRead = max(0, r.CacheRead-int64(old.CacheReadTokens))
		r.CacheWrite = max(0, r.CacheWrite-int64(old.CacheWriteTokens))
		r.Reasoning = max(0, r.Reasoning-int64(old.ReasoningTokens))
		e = costobserver.NativeEntry(base, a.state.Provider, r)
		e.LLMCallCount = 0
		e.EventID += ":growth"
		e.IdempotencyKey = e.EventID
		e.OperationMetadata["native_usage_adjustment"] = true
	}
	return e
}
func nativeFresh(e costledger.Entry) int {
	if includes, _ := e.OperationMetadata["prompt_tokens_include_cache"].(bool); includes {
		return max(0, e.PromptTokens-e.CacheReadTokens-e.CacheWriteTokens)
	}
	return e.PromptTokens
}
func nativeReconciles(base costledger.Entry, records []nativeusage.Record) bool {
	var in, out, read, write int64
	for _, r := range records {
		v := r.Input
		if r.InputIncludesCache {
			v -= r.CacheRead + r.CacheWrite
		}
		in += v
		out += r.Output
		read += r.CacheRead
		write += r.CacheWrite
	}
	return len(records) > 0 && in == int64(nativeFresh(base)) && out == int64(base.CompletionTokens) && read == int64(base.CacheReadTokens) && write == int64(base.CacheWriteTokens)
}

func nativeSourceRewound(c costledger.NativeCoverage, s nativeusage.Snapshot) bool {
	for path, old := range c.Positions {
		if now, ok := s.Positions[path]; ok && now < old {
			return true
		}
	}
	return false
}

// Record preserves the foreground charge unless native coverage is complete
// and reconciled. Its source receipt is committed in the same transaction.
func (a *nativeCostAccountant) Record(base costledger.Entry) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.retired {
		return false
	}
	s, err := a.read()
	c, e := a.api.costLedger.NativeCoverage(a.runtimeKey())
	if e != nil {
		a.reportGap("ledger_receipt_read")
		a.state.PendingForeground = true
		a.save()
		return false
	}
	if c.Fallback == nil {
		c.Fallback = map[string]costledger.Entry{}
	}
	var records []nativeusage.Record
	for _, r := range s.Records {
		if r.Timestamp.IsZero() || r.Timestamp.Before(a.state.Current.Timestamp) || r.Timestamp.After(base.Timestamp) {
			continue
		}
		records = append(records, r)
	}
	complete := err == nil && s.Supported && s.CaughtUp && s.Gaps == 0 && !nativeSourceRewound(c, s)
	entries := []costledger.Entry{base}
	mode := "foreground"
	priced := true
	for _, r := range records {
		if costobserver.NativeEntry(a.contextFor(r), a.state.Provider, r).BillingBasis == "unpriced" && base.BillingBasis != "unpriced" {
			priced = false
		}
	}
	if complete && priced && nativeReconciles(base, records) {
		entries = nil
		mode = "native"
		for _, r := range records {
			entries = append(entries, a.entry(r, c))
		}
	} else {
		if base.Timestamp.After(c.ForegroundThrough) {
			c.ForegroundThrough = base.Timestamp
		}
		for _, r := range records {
			entry := costobserver.NativeEntry(a.contextFor(r), a.state.Provider, r)
			c.Fallback[entry.IdempotencyKey] = entry
		}
		if !complete {
			a.reportGap("source_unavailable_foreground_preserved")
		} else if nativeExceeds(base, records) {
			a.reportGap("source_exceeds_foreground_preserved")
		} else {
			a.reportGap("source_totals_unreconciled_foreground_preserved")
		}
	}
	c.Ready = complete && len(s.Records) > 0
	if err == nil && len(s.Positions) > 0 {
		positions := map[string]int64{}
		for path, offset := range c.Positions {
			positions[path] = offset
		}
		for _, r := range s.Records {
			if !r.Timestamp.IsZero() && !r.Timestamp.After(base.Timestamp) && r.Offset > positions[r.Path] {
				positions[r.Path] = r.Offset
			}
		}
		c.Positions = positions
	}
	if err := a.api.costLedger.CommitNativeUsage(a.runtimeKey(), entries, c); err != nil {
		a.reportGap("ledger_commit_foreground_preserved")
		a.state.PendingForeground = true
		a.save()
		return false
	}
	a.state.PendingForeground = false
	a.stamp = ""
	a.save()
	a.copyEntries(entries, c)
	log.Printf("[NATIVE_COST] generation session=%s provider=%s native=%s execution=%s mode=%s sources=%d entries=%d caught_up=%t gaps=%d", base.SessionID, a.state.Provider, a.state.NativeID, base.ExecutionID, mode, len(records), len(entries), s.CaughtUp, s.Gaps)
	return true
}

func nativeExceeds(base costledger.Entry, records []nativeusage.Record) bool {
	var in, out int64
	for _, r := range records {
		v := r.Input
		if r.InputIncludesCache {
			v -= r.CacheRead + r.CacheWrite
		}
		in += v
		out += r.Output
	}
	return in > int64(nativeFresh(base)) || out > int64(base.CompletionTokens)
}

func (a *nativeCostAccountant) copyEntries(entries []costledger.Entry, c costledger.NativeCoverage) {
	for _, entry := range entries {
		for _, destination := range costledger.WorkspaceCostCopies(entry.WorkflowID) {
			if l, err := costledger.WorkspaceLedger(destination); err == nil && l != nil {
				if err := l.CommitNativeUsage(a.runtimeKey(), []costledger.Entry{entry}, c); err != nil {
					a.reportGap("workspace_ledger_copy")
				}
			}
		}
	}
}

func (a *nativeCostAccountant) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.poll()
		}
	}
}
func (a *nativeCostAccountant) poll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.retired {
		return
	}
	if session, ok := mcpagent.LookupSession(a.state.Current.SessionID); ok && session.ActiveTurnID() != "" {
		return
	}
	if err := a.bind(); err != nil {
		return
	}
	if stamp, err := nativeusage.SourceStamp(a.state.Provider, a.state.Path); err == nil && stamp != "" && stamp == a.stamp {
		return
	}
	s, err := a.read()
	if err != nil || !s.CaughtUp || s.Gaps > 0 {
		a.reportGap("runtime_source_gap")
		return
	}
	c, err := a.api.costLedger.NativeCoverage(a.runtimeKey())
	if err != nil {
		a.reportGap("ledger_receipt_read")
		return
	}
	if nativeSourceRewound(c, s) {
		a.reportGap("source_rewound_foreground_preserved")
		return
	}
	if !c.Ready || a.state.PendingForeground {
		// This is the coverage handoff, not a historical replay. The existing
		// foreground path owns all records through this proven source boundary.
		c.Ready = true
		c.Positions = s.Positions
		if err := a.api.costLedger.CommitNativeUsage(a.runtimeKey(), nil, c); err != nil {
			a.reportGap("handoff_receipt_write")
			return
		}
		a.state.PendingForeground = false
		a.stamp = s.SourceStamp
		a.save()
		log.Printf("[NATIVE_COST] handoff session=%s provider=%s native=%s files=%d", a.state.Current.SessionID, a.state.Provider, a.state.NativeID, len(c.Positions))
		return
	}
	c.Ready = true
	var entries []costledger.Entry
	for _, r := range s.Records {
		if r.Timestamp.IsZero() || r.Timestamp.Before(a.state.Floor) || !r.Timestamp.After(c.ForegroundThrough) || r.Offset <= c.Positions[r.Path] {
			continue
		}
		entries = append(entries, a.entry(r, c))
	}
	if len(entries) == 0 {
		a.stamp = s.SourceStamp
		return
	}
	c.Positions = s.Positions
	if err := a.api.costLedger.CommitNativeUsage(a.runtimeKey(), entries, c); err != nil {
		a.reportGap("runtime_ledger_commit")
		return
	}
	a.save()
	a.stamp = s.SourceStamp
	a.copyEntries(entries, c)
	log.Printf("[NATIVE_COST] runtime_saved session=%s provider=%s native=%s entries=%d", a.state.Current.SessionID, a.state.Provider, a.state.NativeID, len(entries))
}
