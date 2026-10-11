// Package producthost adapts the public platform to separately compiled products.
package producthost

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

type Store struct{ store *store.MemoryStore }

func New(st *store.MemoryStore) *Store { return &Store{store: st} }
func (s *Store) GetUser(id string) (api.User, bool) {
	u, ok := s.store.GetUser(id)
	return api.User{ID: u.ID, WorkspaceID: u.WorkspaceID, Email: u.Email}, ok
}
func (s *Store) GetGroup(id string) (api.Group, bool) {
	g, ok := s.store.GetGroup(id)
	return api.Group{ID: g.ID, WorkspaceID: g.WorkspaceID, Name: g.Name}, ok
}
func (s *Store) GroupsOf(id string) []string { return s.store.GroupsOf(id) }
func (s *Store) ListUsers(w string) []api.User {
	out := []api.User{}
	for _, u := range s.store.ListUsers(w) {
		out = append(out, api.User{ID: u.ID, WorkspaceID: u.WorkspaceID, Email: u.Email})
	}
	return out
}
func (s *Store) PersistenceError() error { return s.store.PersistenceError() }
func (s *Store) GetLLMModelAccess(w, m string) (api.LLMModelAccess, bool) {
	row := s.store.ExtensionState("llm-gateway", w, m)
	if row.Revision == 0 {
		return api.LLMModelAccess{}, false
	}
	var a api.LLMModelAccess
	if json.Unmarshal(row.Data, &a) != nil {
		return api.LLMModelAccess{ModelID: m, Revision: row.Revision}, true
	}
	a.Revision = row.Revision
	return a, true
}
func (s *Store) SetLLMModelAccessWithDenials(w, m string, users, groups, denied []string, r int, actor string) (api.LLMModelAccess, error) {
	if denied == nil {
		old, _ := s.GetLLMModelAccess(w, m)
		denied = old.DeniedUserIDs
	}
	for _, id := range append(slices.Clone(users), denied...) {
		u, ok := s.GetUser(id)
		if !ok || u.WorkspaceID != w {
			return api.LLMModelAccess{}, errors.New("unknown user")
		}
	}
	for _, id := range denied {
		if slices.Contains(users, id) {
			return api.LLMModelAccess{}, errors.New("conflicting permission")
		}
	}
	for _, id := range groups {
		g, ok := s.GetGroup(id)
		if !ok || g.WorkspaceID != w {
			return api.LLMModelAccess{}, errors.New("unknown group")
		}
	}
	a := api.LLMModelAccess{ModelID: m, UserIDs: slices.Clone(users), GroupIDs: slices.Clone(groups), DeniedUserIDs: slices.Clone(denied)}
	b, _ := json.Marshal(a)
	row, err := s.store.CompareAndSwapExtensionState("llm-gateway", w, m, b, r)
	if errors.Is(err, store.ErrExtensionStateConflict) {
		err = api.ErrRevisionConflict
	}
	a.Revision = row.Revision
	return a, err
}
func (s *Store) BeginAudit(e api.AuditEvent) error {
	info := s.store.AuditInfo()
	if !info.Enabled || info.WriteMode != "durable" || !info.WriteHealthy {
		return errors.New("durable host audit required")
	}
	e.Phase = "started"
	return s.AppendAudit(e)
}
func (s *Store) AppendAudit(e api.AuditEvent) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	id := e.ID
	if e.Phase == "started" {
		id += ":admission"
	}
	return s.store.AppendAudit(store.AuditEvent{ID: id, CallID: e.CallID, Timestamp: e.Timestamp, WorkspaceID: e.WorkspaceID, UserID: e.UserID, GroupIDs: e.GroupIDs, ClientID: e.ClientID, ConnectorID: e.ConnectorID, PublicName: e.PublicName, UpstreamName: e.UpstreamName, Decision: e.Decision, Outcome: e.Outcome, DurationMs: e.DurationMs, ErrorText: e.ErrorText, Output: payload})
}
func (s *Store) Query(ctx context.Context, f api.AuditFilter) ([]api.AuditEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Fetch retained product records before applying the display limit; admission
	// and completion share a logical ID, so they cannot inflate cost totals.
	// Status predicates must run after admission/completion coalescing. Applying
	// them to raw rows could resurrect an admission whose completed status changed.
	rows, err := s.store.ReadAudit(store.AuditFilter{
		WorkspaceID: f.WorkspaceID, UserID: f.UserID, GroupID: f.GroupID,
		ClientID: f.ClientID, ConnectorID: f.ConnectorID, PublicName: f.PublicName,
		After: f.After, Before: f.Before,
	})
	if err != nil {
		return nil, err
	}
	out := []api.AuditEvent{}
	indices := map[string]int{}
	for _, row := range rows {
		var e api.AuditEvent
		if strings.HasPrefix(row.ID, "llm-") {
			if json.Unmarshal(row.Output, &e) != nil || e.ID == "" {
				continue
			}
		} else {
			e = api.AuditEvent{
				ID: row.ID, CallID: row.CallID, Timestamp: row.Timestamp,
				WorkspaceID: row.WorkspaceID, UserID: row.UserID, GroupIDs: row.GroupIDs,
				ClientID: row.ClientID, ConnectorID: row.ConnectorID, PublicName: row.PublicName,
				UpstreamName: row.UpstreamName, Decision: row.Decision, Outcome: row.Outcome,
				DurationMs: row.DurationMs, ErrorText: row.ErrorText, Input: row.Input, Output: row.Output,
				InputTruncated: row.InputTruncated, OutputTruncated: row.OutputTruncated,
			}
		}
		if index, exists := indices[e.ID]; exists {
			// Completion always wins, regardless of backend ordering. Keep an
			// unmatched admission visible after a crash; it is not a billed total.
			if out[index].Phase == "started" && e.Phase != "started" {
				out[index] = e
			}
			continue
		}
		indices[e.ID] = len(out)
		out = append(out, e)
	}
	filtered := out[:0]
	for _, e := range out {
		isLLM := strings.HasPrefix(e.ID, "llm-")
		if (f.Kind != "" && f.Kind != "llm" && f.Kind != "mcp") ||
			(f.Kind == "llm" && !isLLM) || (f.Kind == "mcp" && isLLM) ||
			(f.Decision != "" && e.Decision != f.Decision) ||
			(f.Outcome != "" && e.Outcome != f.Outcome) {
			continue
		}
		filtered = append(filtered, e)
	}
	out = filtered
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}
func (s *Store) Summary(ctx context.Context, f api.AuditFilter) (api.AuditSummary, error) {
	f.Limit = 0
	events, err := s.Query(ctx, f)
	if err != nil {
		return api.AuditSummary{}, err
	}
	return summarizeAudit(events), nil
}
