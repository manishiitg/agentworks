package server

import (
	"path/filepath"
	"slices"
	"testing"
)

// #3210: features work without a Team, and a task finds its feature by name, by the
// feature's issue link, or through its parent; a sender in one feature defaults to it
// unless its issue is another feature's link.
func TestFeaturesWithoutTeamLinkTasks(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	const project = "crew-features-test"
	agent := crewTeamRegisteredAgent{OwnerID: "owner", ChatKey: "chat-a", Name: "builder"}
	if _, err := crewFeatureDo(project, "owner", agent, nil, agentMessageStore{}, crewWorkInput{Op: "feature_join", Feature: "Feature panels"}); err != nil {
		t.Fatal(err)
	}
	if _, err := crewFeatureDo(project, "owner", crewTeamRegisteredAgent{OwnerID: "owner", ChatKey: "chat-b"}, nil, agentMessageStore{}, crewWorkInput{Op: "feature_join", Feature: "Messaging", Link: "12"}); err != nil {
		t.Fatal(err)
	}
	features := readCrewFeatures(project)
	if len(features) != 2 || features[1].Link != "#12" {
		t.Fatalf("features = %+v", features)
	}
	tasks := crewFeatureAssign(project, []crewTeamTask{
		{ID: "a", featureKey: "feature panels"},
		{ID: "b", Group: "#12"},
		{ID: "c", Parent: "b"},
		{ID: "d", Group: "#99"},
	})
	want := map[string]string{"a": features[0].ID, "b": features[1].ID, "c": features[1].ID, "d": ""}
	for _, task := range tasks {
		if task.Feature != want[task.ID] {
			t.Fatalf("task %s feature = %q, want %q", task.ID, task.Feature, want[task.ID])
		}
	}
	if got := crewFeatureDefaultIn(project, "chat-a", ""); got != features[0].ID {
		t.Fatalf("default for a sender in one feature = %q", got)
	}
	if got := crewFeatureDefaultIn(project, "chat-a", "#12"); got != "" {
		t.Fatalf("an issue that is another feature's link must win, got %q", got)
	}
	// An Only-me agent's finished task enters the story for its person only.
	private := []crewTeamTask{{ID: "p", Title: "private work", State: "done", Feature: features[0].ID, privateTo: "owner"}}
	shown := func(viewer string) bool {
		f := readCrewFeatures(project)[0]
		story := crewFeatureView(project, f, private, agentMessageStore{}, viewer, nil)["story"].([]crewFeatureEvent)
		return slices.ContainsFunc(story, func(e crewFeatureEvent) bool { return e.Text == "private work" })
	}
	if !shown("owner") || shown("teammate") {
		t.Fatal("a private task's story line must show to its person only")
	}
}
