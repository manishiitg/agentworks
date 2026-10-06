package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Organize Brain is a built-in Brain schedule: off by default, and each person sets their own cadence (PLAT-618).
func TestOrganizeBrainScheduleCadenceIsPerPerson(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(knowledgebaseproduct.BuiltinAgentProfile()); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	svc := NewProductScheduleService(nil, registry)
	svc.users = func(string) []string { return []string{"u1", "u2"} }
	svc.readFile = func(_ context.Context, path string) (string, bool, error) { c, ok := files[path]; return c, ok, nil }
	svc.writeFile = func(_ context.Context, path, content string) error { files[path] = content; return nil }
	ctx := context.Background()
	id := productScheduleJobID(knowledgebaseproduct.ProfileID, "organize")
	job, err := svc.Job(ctx, "u1", id)
	if err != nil || job.Effective().Enabled || job.Effective().CadenceHours != 168 || !job.Schedule.Isolated {
		t.Fatalf("Organize Brain must be an isolated weekly schedule, off by default: %+v %v", job.Effective(), err)
	}
	if job, err = svc.SetCadence(ctx, "u1", id, 72); err != nil || job.Effective().CadenceHours != 72 {
		t.Fatalf("every 3 days: %+v %v", job.Effective(), err)
	}
	if other, _ := svc.Job(ctx, "u2", id); other.Effective().CadenceHours != 168 {
		t.Fatalf("another person's cadence must not change: %d", other.Effective().CadenceHours)
	}
	if _, err := svc.SetCadence(ctx, "u1", id, 0); err == nil {
		t.Fatal("a zero cadence must be refused")
	}
}
