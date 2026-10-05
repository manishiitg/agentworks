package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

func TestProductScheduleJobIDRoundTrip(t *testing.T) {
	id := productScheduleJobID("video-studio", "daily-checkin")
	if id != "product:video-studio:daily-checkin" {
		t.Fatalf("id = %q", id)
	}
	profile, sched, ok := parseProductScheduleJobID(id)
	if !ok || profile != "video-studio" || sched != "daily-checkin" {
		t.Fatalf("parse = %q %q %v", profile, sched, ok)
	}
	for _, bad := range []string{"daily-checkin", "product:", "product:x", "product:x:"} {
		if _, _, ok := parseProductScheduleJobID(bad); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

func TestProjectScheduleJobIDRoundTrip(t *testing.T) {
	id := projectScheduleJobID("work", "project-1", "daily-checkin")
	if id != "product-project:work:project-1:daily-checkin" {
		t.Fatalf("id = %q", id)
	}
	profile, project, sched, ok := parseProjectScheduleJobID(id)
	if !ok || profile != "work" || project != "project-1" || sched != "daily-checkin" {
		t.Fatalf("parse = %q %q %q %v", profile, project, sched, ok)
	}
	for _, bad := range []string{"daily-checkin", "product-project:", "product-project:work:project-1", "product-project:work::daily"} {
		if _, _, _, ok := parseProjectScheduleJobID(bad); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

func TestProductScheduleJobActivationBaselinePrecedence(t *testing.T) {
	manifestTime := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	createdTime := manifestTime.Add(time.Hour)
	stateTime := createdTime.Add(time.Hour)
	job := productScheduleJob{
		Schedule:            productschedule.Schedule{CreatedAt: createdTime.Format(time.RFC3339)},
		ManifestActivatedAt: manifestTime,
	}
	if got := job.activatedAt(); !got.Equal(createdTime) {
		t.Fatalf("schedule creation should beat manifest fallback: got %s want %s", got, createdTime)
	}
	job.State.ActivatedAt = stateTime.Format(time.RFC3339)
	if got := job.activatedAt(); !got.Equal(stateTime) {
		t.Fatalf("persisted activation should be authoritative: got %s want %s", got, stateTime)
	}
}

func TestRunDestinationDefaultsToCrewChatAndAcceptsIsolated(t *testing.T) {
	for _, test := range []struct {
		value    string
		isolated bool
	}{{"", false}, {runDestinationCrewChat, false}, {runDestinationIsolated, true}} {
		got, err := isolatedForRunDestination(test.value)
		if err != nil || got != test.isolated {
			t.Fatalf("destination %q = %v, %v", test.value, got, err)
		}
	}
	if _, err := isolatedForRunDestination("new_chat"); err == nil {
		t.Fatal("invalid destination accepted")
	}
}

func TestUsersWithProductFollowsDirectoryAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "work")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin1","username":"admin","admin":true,"can_create":true,"products":[]},
		{"id":"member1","username":"m","can_create":true,"products":[]},
		{"id":"scoped1","username":"s","can_create":true,"products":["finance"]},
		{"id":"reader1","username":"r","can_create":false,"products":[]},
		{"id":"off1","username":"o","admin":true,"disabled":true}
	]}`)
	got := usersWithProduct("video-studio")
	if strings.Join(got, ",") != "admin1,member1" {
		t.Fatalf("video-studio users = %v", got)
	}
	if got := usersWithProduct("finance"); strings.Join(got, ",") != "admin1,member1,scoped1" {
		t.Fatalf("finance users = %v", got)
	}
	if got := usersWithProduct("work"); strings.Join(got, ",") != "admin1" {
		t.Fatalf("admin-only Work users = %v", got)
	}
}

func TestUsersWithProductSingleUserMode(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	got := usersWithProduct("anything")
	if len(got) != 1 || got[0] != GetDefaultUserID() {
		t.Fatalf("single-user mode should return the default user, got %v", got)
	}
}

func newProductScheduleTestService(t *testing.T, users []string) (*ProductScheduleService, map[string]string) {
	t.Helper()
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "family", Name: "Family", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "family",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeSingleton},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeFixed, Root: "Chats"},
		},
		Schedules: []productschedule.Schedule{
			{ID: "checkin", Name: "Check-in", Enabled: true, CronExpression: "0 8 * * *", Timezone: "UTC", Messages: []string{"review", "summarize"}},
			{ID: "weekly", Name: "Weekly", Enabled: false, CronExpression: "0 9 * * 1", Timezone: "UTC", Messages: []string{"weekly"}},
		},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	svc := NewProductScheduleService(nil, registry)
	svc.users = func(string) []string { return users }
	svc.readFile = func(_ context.Context, path string) (string, bool, error) {
		c, ok := files[path]
		return c, ok, nil
	}
	svc.writeFile = func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}
	return svc, files
}

func TestProductScheduleJobsForUserAndEnableOverride(t *testing.T) {
	svc, files := newProductScheduleTestService(t, []string{"u1"})
	ctx := context.Background()
	jobs, err := svc.JobsForUser(ctx, "u1")
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs = %d err = %v", len(jobs), err)
	}
	if _, err := svc.JobsForUser(ctx, "u2"); err != nil {
		t.Fatal(err)
	} else if j, _ := svc.JobsForUser(ctx, "u2"); len(j) != 0 {
		t.Fatalf("a user without the product must see no jobs: %d", len(j))
	}

	job, err := svc.SetEnabled(ctx, "u1", "product:family:checkin", false)
	if err != nil {
		t.Fatal(err)
	}
	if job.Effective().Enabled {
		t.Fatal("override should disable the schedule")
	}
	if !strings.Contains(files[productScheduleStatePath("u1")], `"enabled": false`) {
		t.Fatalf("override not persisted: %s", files[productScheduleStatePath("u1")])
	}
	job, err = svc.SetEnabled(ctx, "u1", "product:family:weekly", true)
	if err != nil || !job.Effective().Enabled {
		t.Fatalf("override should enable the product-disabled schedule: %+v %v", job, err)
	}
	if _, err := svc.Job(ctx, "u1", "product:family:nope"); err == nil {
		t.Fatal("unknown schedule must not resolve")
	}
}

func TestProductScheduleJobResponseShape(t *testing.T) {
	svc, _ := newProductScheduleTestService(t, []string{"u1"})
	job, err := svc.Job(context.Background(), "u1", "product:family:checkin")
	if err != nil {
		t.Fatal(err)
	}
	job.State.LastRunAt = time.Now().Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	job.State.LastStatus = "success"
	resp := svc.jobResponse(job, "_users/u1/Chats")
	if resp.EntityType != "product" || resp.ID != "product:family:checkin" || resp.WorkflowLabel != "Family" || len(resp.Messages) != 2 || !resp.Enabled {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.LastRunAt == nil || resp.NextRunAt == nil || !resp.NextRunAt.After(*resp.LastRunAt) {
		t.Fatalf("last/next run not populated: %+v", resp)
	}
	if resp.LastStatus != "success" {
		t.Fatalf("last status = %q", resp.LastStatus)
	}

	// A one-time schedule is shown as a calendar entry with its next run, and has none once it ran.
	at := time.Now().Add(3 * time.Hour).UTC()
	once := job
	once.Schedule.CronExpression, once.Schedule.CadenceHours = "", 0
	once.Schedule.RunAt, once.Schedule.Timezone = at.Format(time.RFC3339), "Asia/Kolkata"
	once.State.LastRunAt = ""
	resp = svc.jobResponse(once, "_users/u1/Chats")
	local := at.In(time.FixedZone("IST", 5*3600+1800))
	if resp.ScheduleType != "calendar" || len(resp.CalendarItems) != 1 || resp.CalendarItems[0].Date != local.Format("2006-01-02") ||
		resp.CalendarItems[0].Time != local.Format("15:04") || resp.NextRunAt == nil || !resp.NextRunAt.Equal(at.Truncate(time.Second)) {
		t.Fatalf("one-time response = %+v", resp)
	}
	once.State.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	if resp = svc.jobResponse(once, "_users/u1/Chats"); resp.NextRunAt != nil {
		t.Fatalf("a one-time schedule that ran still shows a next run: %+v", resp)
	}
}

func TestAgentProfileSchedulesRequireSingletonConversation(t *testing.T) {
	profile := agentprofiles.Profile{
		ID: "p", Name: "P", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true,
		Runtime:   agentprofiles.RuntimePolicy{Transport: "auto", Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}},
		Schedules: []productschedule.Schedule{{ID: "a", Name: "A", CronExpression: "0 8 * * *", Messages: []string{"m"}}},
	}
	if err := agentprofiles.Validate(profile); err == nil || !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("keyed profiles must reject schedules, got %v", err)
	}
}
