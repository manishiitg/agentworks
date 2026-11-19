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
	// #3210: a feature can be renamed (not onto another's name), and a done feature gives its issue link to the
	// feature that takes it (seen live: a replacement feature could not take #3210 from a finished one).
	do := func(in crewWorkInput) error {
		_, err := crewFeatureDo(project, "owner", agent, nil, agentMessageStore{}, in)
		return err
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Feature panels", Rename: "messaging"}); err == nil {
		t.Fatal("rename onto another feature's name was allowed")
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Feature panels", Rename: "Panels", Link: "12"}); err == nil {
		t.Fatal("an active feature's link was taken")
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Messaging", State: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Feature panels", Rename: "Panels", Link: "12"}); err != nil {
		t.Fatal(err)
	}
	if f := readCrewFeatures(project); f[0].Name != "Panels" || f[0].Link != "#12" || f[1].Link != "" {
		t.Fatalf("after rename and link move: %+v", f)
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Panels", Rename: "Feature panels"}); err != nil {
		t.Fatal(err)
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Messaging", State: "active", Link: "12"}); err == nil {
		t.Fatal("an active feature's link was taken by a reopened one")
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Feature panels", Link: "15"}); err != nil {
		t.Fatal(err)
	}
	if err := do(crewWorkInput{Op: "feature_update", Feature: "Messaging", Link: "12"}); err != nil {
		t.Fatal(err)
	}
	// An Only-me agent's finished task enters the story for its person only.
	// The line names the agent and whose agent it is, so the feature shows who worked on it.
	private := []crewTeamTask{{ID: "p", Title: "private work", State: "done", Agent: "helper", Person: "Ana", Feature: features[0].ID, privateTo: "owner"}}
	shown := func(viewer string) bool {
		f := readCrewFeatures(project)[0]
		story := crewFeatureView(project, f, private, agentMessageStore{}, viewer, nil)["story"].([]crewFeatureEvent)
		return slices.ContainsFunc(story, func(e crewFeatureEvent) bool { return e.Text == "private work" && e.By == "helper" && e.Person == "Ana" })
	}
	if !shown("owner") || shown("teammate") {
		t.Fatal("a private task's story line must show to its person only")
	}
	// #3210 handover: a Mac Crew's features join its Team's when the Team turns on. The
	// same name is the same feature, the Mac's chat keys become the Team's, a line only
	// one person saw stays theirs, and sending twice adds nothing.
	const team = "crew-features-team"
	if _, err := crewFeatureDo(team, "owner", crewTeamRegisteredAgent{OwnerID: "owner", ChatKey: "chat-t"}, nil, agentMessageStore{}, crewWorkInput{Op: "feature_join", Feature: "feature panels"}); err != nil {
		t.Fatal(err)
	}
	if _, err := crewFeatureDo(project, "owner", crewTeamRegisteredAgent{OwnerID: "owner", ChatKey: project + codeChatSideMarker + "tab1", Name: "mac"}, nil, agentMessageStore{}, crewWorkInput{Op: "feature_join", Feature: "Feature panels"}); err != nil {
		t.Fatal(err)
	}
	hand := crewFeatureHandOff{LocalProject: project, Features: readCrewFeatures(project)}
	for i := range hand.Features {
		hand.Features[i].Story = crewFeatureStory(project, hand.Features[i].ID, "*", "", 0)
	}
	for range 2 {
		if _, err := crewFeaturesMerge(team, hand); err != nil {
			t.Fatal(err)
		}
	}
	got := readCrewFeatures(team)
	panels, messaging := got[crewFeatureFind(got, "Feature panels")], got[crewFeatureFind(got, "Messaging")]
	if len(got) != 2 || !slices.Contains(panels.Agents, team+codeChatSideMarker+"tab1") || !slices.Contains(panels.Agents, "chat-t") || messaging.Link != "#12" {
		t.Fatalf("handed over = %+v", got)
	}
	story := func(viewer string) []crewFeatureEvent { return crewFeatureStory(team, panels.ID, viewer, "", 0) }
	if !slices.ContainsFunc(story("owner"), func(e crewFeatureEvent) bool { return e.Text == "private work" }) || slices.ContainsFunc(story("teammate"), func(e crewFeatureEvent) bool { return e.Owner != "" }) {
		t.Fatal("a line only its person saw must stay theirs after the handover")
	}
	if n := len(story("owner")); n != len(crewFeatureStory(project, features[0].ID, "*", "", 0))+1 {
		t.Fatalf("story lines after two hand-offs = %d", n)
	}
}
