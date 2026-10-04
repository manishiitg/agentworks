package server

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// scanCrewReferences counts, for each Crew folder, the files in the stores that hold references to a Crew's path and
// mention it. The move rewrites none of them: every old spelling keeps resolving through the owner registry's alias
// (and the workspace transport and the readers listed in PLAT-442), so the counts tell the operator what will be served
// through the alias. It reads, never writes, never follows a link, and skips files over 8 MiB.
func scanCrewReferences(docs *os.Root, folders []string) map[string][]crewReferenceHit {
	out := map[string][]crewReferenceHit{}
	if len(folders) == 0 {
		return out
	}
	counts := map[string]map[string]int{}
	note := func(folder, kind string) {
		if counts[folder] == nil {
			counts[folder] = map[string]int{}
		}
		counts[folder][kind]++
	}
	// scanFile counts the Crews a file mentions; skip names the file is the Crew's own (its own folder is not a reference).
	scanFile := func(rel, kind string, info fs.FileInfo, skip ...string) {
		if !info.Mode().IsRegular() || info.Size() > 8<<20 || info.Size() == 0 {
			return
		}
		file, err := docs.Open(rel)
		if err != nil {
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, 8<<20))
		_ = file.Close()
		if err != nil {
			return
		}
	next:
		for _, folder := range folders {
			for _, own := range skip {
				if own == folder {
					continue next
				}
			}
			// A Crew's folder name is its slug and the first 8 characters of its id: unique in practice.
			if bytes.Contains(data, []byte(folder)) {
				note(folder, kind)
			}
		}
	}
	var walk func(rel, kind string, depth int)
	walk = func(rel, kind string, depth int) {
		handle, err := docs.Open(rel)
		if err != nil {
			return
		}
		names, err := handle.Readdirnames(-1)
		_ = handle.Close()
		if err != nil {
			return
		}
		sort.Strings(names)
		for _, name := range names {
			child := rel + "/" + name
			info, err := docs.Lstat(child)
			if err != nil || info.Mode()&fs.ModeSymlink != 0 {
				continue
			}
			if info.IsDir() {
				if depth > 0 {
					walk(child, kind, depth-1)
				}
				continue
			}
			scanFile(child, kind, info)
		}
	}
	// Workflows: attached Crews and context paths.
	if names := dirNames(docs, "Workflow"); len(names) > 0 {
		for _, name := range names {
			if info, err := docs.Lstat("Workflow/" + name + "/workflow.json"); err == nil {
				scanFile("Workflow/"+name+"/workflow.json", "workflow attachments and context paths", info)
			}
		}
	}
	// Server configuration (bot routes, Slack apps, schedules' state).
	for _, root := range []string{"config", "_system"} {
		if info, err := docs.Lstat(root); err == nil && info.IsDir() {
			walk(root, "configuration (bots, schedules, connections)", 3)
		}
	}
	for _, user := range dirNames(docs, workspaceref.UsersDir) {
		base := workspaceref.UserRoot(user)
		// Chat history, submissions, central conversation registry, schedule state.
		if info, err := docs.Lstat(base + "/chat_history"); err == nil && info.IsDir() {
			walk(base+"/chat_history", "chat history, conversations and schedule state", 4)
		}
		// Other Crews' context paths.
		for _, folder := range dirNames(docs, base+"/Chats/Work/projects") {
			if info, err := docs.Lstat(base + "/Chats/Work/projects/" + folder + "/workflow.json"); err == nil {
				scanFile(base+"/Chats/Work/projects/"+folder+"/workflow.json", "other Crews' context paths", info, folder)
			}
		}
	}
	for folder, kinds := range counts {
		names := make([]string, 0, len(kinds))
		for kind := range kinds {
			names = append(names, kind)
		}
		sort.Strings(names)
		for _, kind := range names {
			out[folder] = append(out[folder], crewReferenceHit{Kind: kind, Files: kinds[kind]})
		}
	}
	return out
}

// dirNames lists the real directories directly below docs/rel (symlinks are not followed or listed).
func dirNames(docs *os.Root, rel string) []string {
	info, err := docs.Lstat(rel)
	if err != nil || !info.IsDir() {
		return nil
	}
	handle, err := docs.Open(rel)
	if err != nil {
		return nil
	}
	defer handle.Close()
	names, err := handle.Readdirnames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range names {
		if info, err := docs.Lstat(rel + "/" + name); err == nil && info.IsDir() && !strings.HasPrefix(name, ".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
