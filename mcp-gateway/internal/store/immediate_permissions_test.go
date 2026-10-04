package store

import (
	"errors"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
)

func TestImmediatePermissionsValidationVersionsAndPersistence(t *testing.T) {
	s, path := sqlFixture(t)
	p := access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "Project restrictions", Rules: []access.ToolRule{{PublicName: "c__read", Fingerprint: "f", Conditions: []access.Condition{{Path: "/project", Op: "matches", Value: "one|two", Description: "Only projects one and two are allowed"}}}}}
	missing := access.Clone(p)
	missing.Rules[0].Conditions[0].Description = "  "
	if _, err := s.SaveAccessPackage(missing, 0, "admin"); err == nil {
		t.Fatal("regex without description accepted")
	}
	saved, err := s.SaveAccessPackage(p, 0, "admin")
	if err != nil || saved.Status != "published" || saved.Version != 1 {
		t.Fatal(saved, err)
	}
	live, governed := s.PolicyForTool("w", "c__read")
	if !governed || len(live) != 1 {
		t.Fatal("save did not activate permissions")
	}
	if _, err = s.SaveAccessPackage(p, 0, "other-admin"); !errors.Is(err, ErrPolicyConflict) {
		t.Fatal("stale edit accepted", err)
	}
	invalid := access.Clone(saved)
	invalid.Rules[0].Conditions[0].Value = "["
	if _, err = s.SaveAccessPackage(invalid, 1, "admin"); err == nil {
		t.Fatal("invalid regex applied")
	}
	invalid = access.Clone(saved)
	invalid.Rules[0].Fingerprint = "changed"
	if _, err = s.SaveAccessPackage(invalid, 1, "admin"); err == nil {
		t.Fatal("unapproved fingerprint applied")
	}
	invalid = access.Clone(saved)
	invalid.Rules[0].Conditions[0].Path = "/unknown"
	if _, err = s.SaveAccessPackage(invalid, 1, "admin"); err == nil {
		t.Fatal("unknown schema path applied")
	}
	if events := s.ListPolicyEvents("w"); len(events) != 1 || events[0].Actor != "admin" || events[0].Action != "save_permissions" {
		t.Fatal("invalid save changed history", events)
	}
	// Legacy drafts remain inactive and projection rebuilds retain compatibility.
	legacy, ok := s.SavePackageDraft(access.Package{ID: "legacy", WorkspaceID: "w", GroupID: "g", Name: "Legacy", Rules: p.Rules}, 0)
	if !ok || legacy.Status != "draft" {
		t.Fatal("legacy fixture")
	}
	if len(s.ListAppliedPackages("w")) != 1 {
		t.Fatal("legacy draft exposed")
	}
	if err = s.PersistenceError(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	live, governed = s.PolicyForTool("w", "c__read")
	if !governed || len(live) != 1 || live[0].Version != 1 || live[0].Rules[0].Conditions[0].Value != "one|two" || live[0].Rules[0].Conditions[0].Description != p.Rules[0].Conditions[0].Description {
		t.Fatal("permissions lost or invalid edit survived restart", live)
	}
	if events := s.ListPolicyEvents("w"); len(events) != 1 || events[0].Actor != "admin" {
		t.Fatal("actor event lost on restart", events)
	}
}
