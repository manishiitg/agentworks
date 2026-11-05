package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// ownerVaultFake is the external Vault service for tests of project secrets: each person's own vault (My accounts),
// the old per-project vaults, and runtime grants (a person may use the secrets of the vaults they own). Ownership,
// storage of values, selections and run resolution stay the real server code.
type ownerVaultFake struct {
	mu       sync.Mutex
	personal map[string]string // person -> vault ID
	projects map[string][2]string
	names    map[string][]string // vault ID -> full secret names
}

func newOwnerVaultFake(t *testing.T) *ownerVaultFake {
	t.Helper()
	f := &ownerVaultFake{personal: map[string]string{}, projects: map[string][2]string{}, names: map[string][]string{}}
	token := strings.Repeat("s", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		actor := r.Header.Get("X-CapLayer-Actor")
		path := r.URL.Path
		switch {
		case path == "/api/vaults" && r.Method == http.MethodGet:
			rows := []any{}
			if id := f.personal[actor]; id != "" {
				rows = append(rows, map[string]any{"group": map[string]any{"ID": id, "Name": personalVaultName, "Owners": []string{actor}}, "role": "owner", "members": []string{actor}, "connections": []any{}, "secret_names": f.names[id]})
			}
			for id, project := range f.projects {
				if project[1] == actor {
					rows = append(rows, map[string]any{"group": map[string]any{"ID": id, "Name": "Project", "ProjectRoot": project[0], "Owners": []string{actor}}, "role": "owner", "members": []string{actor}, "connections": []any{}, "secret_names": f.names[id]})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"vaults": rows})
		case path == "/api/vaults" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": f.personalLocked(actor)})
		case path == "/api/project-vaults":
			var in struct {
				Root string `json:"workspace_root"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": f.projectLocked(actor, in.Root)})
		case path == "/api/admin/runtime/secrets":
			rows := []map[string]string{}
			for _, id := range f.ownedLocked(actor) {
				for _, name := range f.names[id] {
					rows = append(rows, map[string]string{"name": name})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"secrets": rows})
		case path == "/api/admin/runtime/servers":
			_, _ = w.Write([]byte(`{"servers":[],"groups":[],"secrets":[]}`))
		case strings.HasPrefix(path, "/api/admin/secrets"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(path, "/api/vaults/"):
			pieces := strings.SplitN(strings.TrimPrefix(path, "/api/vaults/"), "/", 3)
			id := pieces[0]
			if !slices.Contains(f.ownedLocked(actor), id) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			switch {
			case len(pieces) == 1:
				_ = json.NewEncoder(w).Encode(map[string]any{"role": "owner", "secret_names": f.names[id], "group": map[string]any{"ProjectRoot": f.projects[id][0]}})
			case len(pieces) == 2 && pieces[1] == "secrets" && r.Method == http.MethodPost:
				var in struct{ Name string }
				_ = json.NewDecoder(r.Body).Decode(&in)
				if !slices.Contains(f.names[id], in.Name) {
					f.names[id] = append(f.names[id], in.Name)
				}
				_, _ = w.Write([]byte(`{}`))
			case len(pieces) == 3 && pieces[1] == "secrets" && r.Method == http.MethodDelete:
				f.names[id] = slices.DeleteFunc(f.names[id], func(name string) bool { return name == pieces[2] })
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", token)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	managedGlobalsMu.Lock()
	previous := managedGlobals
	managedGlobals = map[string]string{}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() { managedGlobalsMu.Lock(); managedGlobals = previous; managedGlobalsMu.Unlock() })
	return f
}

func (f *ownerVaultFake) personalLocked(person string) string {
	if f.personal[person] == "" {
		f.personal[person] = fmt.Sprintf("v-a%05d", len(f.personal)+1)
	}
	return f.personal[person]
}

func (f *ownerVaultFake) projectLocked(owner, root string) string {
	for id, project := range f.projects {
		if project[0] == root {
			return id
		}
	}
	id := fmt.Sprintf("v-b%05d", len(f.projects)+1)
	f.projects[id] = [2]string{root, owner}
	return id
}

func (f *ownerVaultFake) ownedLocked(person string) []string {
	owned := []string{}
	if id := f.personal[person]; id != "" {
		owned = append(owned, id)
	}
	for id, project := range f.projects {
		if project[1] == person {
			owned = append(owned, id)
		}
	}
	return owned
}

// vaultName is the full name person's own vault gives NAME (creating that vault).
func (f *ownerVaultFake) vaultName(t *testing.T, person, name string) string {
	t.Helper()
	f.mu.Lock()
	id := f.personalLocked(person)
	f.mu.Unlock()
	full, err := vaultSecretName(id, name)
	if err != nil {
		t.Fatal(err)
	}
	return full
}

// seed stores value as NAME in person's own vault, or in root's project vault when root is not empty, and returns its
// full name.
func (f *ownerVaultFake) seed(t *testing.T, api *StreamingAPI, person, root, name, value string) string {
	t.Helper()
	f.mu.Lock()
	id := f.personalLocked(person)
	if root != "" {
		id = f.projectLocked(person, root)
	}
	full, err := vaultSecretName(id, name)
	if err == nil {
		f.names[id] = append(f.names[id], full)
	}
	f.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := api.storeManagedGlobalSecret(context.Background(), full, value, false); err != nil {
		t.Fatal(err)
	}
	return full
}

// has reports whether a vault still lists the full name.
func (f *ownerVaultFake) has(full string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, names := range f.names {
		if slices.Contains(names, full) {
			return true
		}
	}
	return false
}
