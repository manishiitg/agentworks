package server

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"time"
)

// maxUsedInProjects bounds the scan that answers "Used in" for a person's own Vault items.
const maxUsedInProjects = 300

// usedInProject is one project the person builds, with what it selects.
type usedInProject struct {
	Name   string
	Caps   WorkflowCapabilities
	Device string
}

// usedInIndex maps "kind:id" of a Vault item to the names of the person's projects that select it.
type usedInIndex map[string][]string

func buildUsedIn(projects []usedInProject) usedInIndex {
	index := usedInIndex{}
	add := func(key, name string) {
		if !slices.Contains(index[key], name) {
			index[key] = append(index[key], name)
		}
	}
	for _, p := range projects {
		for _, server := range savedAppSelection(p.Caps.SelectedServers) {
			add("mcp:"+strings.TrimPrefix(server, "vault_"), p.Name)
		}
		if p.Caps.SelectedGlobalSecretNames != nil {
			for _, name := range *p.Caps.SelectedGlobalSecretNames {
				add("shared_secret:"+name, p.Name)
			}
		}
		if p.Device != "" {
			add("computer:"+p.Device, p.Name)
		}
		if !p.Caps.ChosenVaultAppsOnly {
			add("all_apps", p.Name)
		}
	}
	return index
}

func (u usedInIndex) forItem(kind, id string) []string {
	out := append([]string{}, u[kind+":"+id]...)
	// An app counts for every project that uses all the person's apps; listing them all is noise, so say it once.
	if kind == "mcp" && len(out) == 0 && len(u["all_apps"]) > 0 {
		return []string{"All your projects (uses all your apps)"}
	}
	return out
}

// personalUsedIn reads the projects the person owns or co-owns (never projects only shared with them): each project's
// own saved selections are the only record of what it uses, so this is a bounded scan, run only when a Vault panel opens.
func (api *StreamingAPI) personalUsedIn(ctx context.Context, person string) usedInIndex {
	start := time.Now()
	crews, err := listAccessibleCrewProjects(ctx, person, "")
	projects := []usedInProject{}
	for _, crew := range crews {
		path, _ := crew["workspace_path"].(string)
		name, _ := crew["name"].(string)
		if crew["access"] != "owner" || path == "" || len(projects) >= maxUsedInProjects {
			continue
		}
		raw, found, e := readProjectRuntimeManifest(ctx, "work", path)
		if e != nil || !found {
			continue
		}
		var saved struct {
			Capabilities WorkflowCapabilities `json:"capabilities"`
		}
		if json.Unmarshal([]byte(raw), &saved) != nil {
			continue
		}
		p := usedInProject{Name: name, Caps: saved.Capabilities}
		if laptop, e := readLaptopSettings(person, path); e == nil {
			p.Device = laptop.DeviceID
		}
		projects = append(projects, p)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond || err != nil {
		log.Printf("[VAULT_USED_IN] slow actor=%q projects=%d elapsed_ms=%d err=%v", person, len(projects), elapsed.Milliseconds(), err)
	}
	return buildUsedIn(projects)
}
