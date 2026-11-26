package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestWriteCrewTeamMonotonicRevision(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")

	initial := crewTeam{
		ProjectID: "team-rev",
		OwnerID:   "owner",
		On:        true,
		Revision:  5,
		People:    []crewTeamPerson{{UserID: "ann"}},
	}
	if err := writeCrewTeam(initial); err != nil {
		t.Fatal(err)
	}
	got, err := readCrewTeam("team-rev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 5 {
		t.Fatalf("initial revision = %d, want 5", got.Revision)
	}

	// Simulate a writer saving a stale copy whose Revision is <= the stored Revision.
	stale := initial
	stale.People = append(stale.People, crewTeamPerson{UserID: "bob"})
	if err := writeCrewTeam(stale); err != nil {
		t.Fatal(err)
	}
	got, err = readCrewTeam("team-rev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 6 {
		t.Fatalf("revision after stale write = %d, want 6", got.Revision)
	}

	// Even a lower revision must advance strictly above disk.
	older := initial
	older.Revision = 2
	if err := writeCrewTeam(older); err != nil {
		t.Fatal(err)
	}
	got, err = readCrewTeam("team-rev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 7 {
		t.Fatalf("revision after older write = %d, want 7", got.Revision)
	}
}

func TestUpdateCrewTeamPreservesConcurrentPeopleAndProjects(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")

	initial := crewTeam{
		ProjectID: "team-rmw",
		OwnerID:   "owner",
		On:        true,
		Revision:  1,
		People:    []crewTeamPerson{{UserID: "ann"}},
	}
	if err := writeCrewTeam(initial); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", idx)
			_, err := updateCrewTeam("team-rmw", func(tm *crewTeam) (bool, error) {
				tm.People = append(tm.People, crewTeamPerson{UserID: userID})
				return true, nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			projID := fmt.Sprintf("proj-%d", idx)
			_, err := updateCrewTeam("team-rmw", func(tm *crewTeam) (bool, error) {
				tm.Projects = append(tm.Projects, crewTeamProject{ProjectID: projID, AddedBy: "owner"})
				return true, nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("updateCrewTeam error: %v", err)
	}

	got, err := readCrewTeam("team-rmw")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.People) != 1+n {
		t.Fatalf("people count = %d, want %d", len(got.People), 1+n)
	}
	if len(got.Projects) != n {
		t.Fatalf("projects count = %d, want %d", len(got.Projects), n)
	}
	for i := 0; i < n; i++ {
		uid := fmt.Sprintf("user-%d", i)
		if !slices.ContainsFunc(got.People, func(p crewTeamPerson) bool { return p.UserID == uid }) {
			t.Fatalf("missing person %s after concurrent updates", uid)
		}
		pid := fmt.Sprintf("proj-%d", i)
		if !got.hasProject(pid) {
			t.Fatalf("missing project %s after concurrent updates", pid)
		}
	}
	if got.Revision != uint64(1+2*n) {
		t.Fatalf("revision = %d, want %d", got.Revision, 1+2*n)
	}
}

func TestAssignCrewTeamAgentIDsSetsAddressOnFirstSave(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]}]}`)

	team := crewTeam{
		ProjectID: "team-addr",
		OwnerID:   "owner",
		On:        true,
		Revision:  1,
	}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}

	people := []crewTeamAgentsPerson{{
		UserID: "owner",
		Name:   "owner",
		Owner:  true,
		Agents: []crewTeamAgent{{
			Tab:     "worker",
			ChatKey: "team-addr::worker",
			Name:    "Worker",
		}},
	}}
	if err := assignCrewTeamAgentIDsWithMain(team, people, ""); err != nil {
		t.Fatal(err)
	}
	if len(people[0].Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(people[0].Agents))
	}
	agent := people[0].Agents[0]
	if agent.ID == "" {
		t.Fatal("expected non-empty agent ID")
	}
	wantAddr := crewTeamAgentBridgeAddress("owner", agent.ID)
	if agent.Address == "" || agent.Address != wantAddr {
		t.Fatalf("first-save agent address = %q, want %q", agent.Address, wantAddr)
	}
}

func TestWriteMacSharedAgentsConcurrentAtomic(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")

	const project = "mac-shared-race"
	var wg sync.WaitGroup
	errs := make(chan error, 12*10)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				var list []macSharedAgent
				if (worker+j)%3 != 0 {
					list = []macSharedAgent{{
						Tab:      fmt.Sprintf("tab-%d-%d", worker, j),
						Name:     fmt.Sprintf("Agent %d", worker),
						Audience: "team",
					}}
				}
				if err := writeMacSharedAgents(project, list); err != nil {
					errs <- err
					return
				}
				_ = readAllMacSharedAgents(project)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent writeMacSharedAgents failed: %v", err)
	}

	path, err := macSharedAgentsPath(project)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %q after concurrent writes", e.Name())
		}
	}
}
