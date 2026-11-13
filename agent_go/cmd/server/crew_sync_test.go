package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// PLAT-856-509 (one sync process, 6 Nov): the file list of a move is checked once, before any byte is assembled. Bytes
// come only from the chunks a change names; a path the sync refuses, two spellings of a name, or a change that does not
// match the list stops the transfer.
func TestCrewContentValidatorRefusesBadFileLists(t *testing.T) {
	hash := strings.Repeat("a", 64)
	file := crewsync.File{Hash: hash, Size: 1}
	chunk := []chunks.Chunk{{ID: strings.Repeat("b", 64), Len: 1}}
	good := crewsync.Snapshot{Files: crewsync.Manifest{"resources/a.md": file}, Changes: []crewsync.Change{{Path: "resources/a.md", After: &file, Chunks: chunk}}}
	if err := validateCrewContent(&good); err != nil {
		t.Fatal(err)
	}
	other := crewsync.File{Hash: strings.Repeat("c", 64), Size: 1}
	for name, s := range map[string]crewsync.Snapshot{
		"excluded path":            {Files: crewsync.Manifest{"resources/.git/config": file}},
		"outside areas":            {Files: crewsync.Manifest{"../a.md": file}},
		"case collision":           {Files: crewsync.Manifest{"resources/Docs/a.md": file, "resources/docs/b.md": file}},
		"change differs from list": {Files: crewsync.Manifest{"resources/a.md": file}, Changes: []crewsync.Change{{Path: "resources/a.md", After: &other, Chunks: chunk}}},
		"chunks do not add up":     {Files: crewsync.Manifest{"resources/a.md": file}, Changes: []crewsync.Change{{Path: "resources/a.md", After: &file}}},
		"unlisted change":          {Files: crewsync.Manifest{}, Changes: []crewsync.Change{{Path: "resources/a.md", After: &file, Chunks: chunk}}},
		"deleted but listed":       {Files: crewsync.Manifest{"resources/a.md": file}, Changes: []crewsync.Change{{Path: "resources/a.md"}}},
		"duplicate change":         {Files: crewsync.Manifest{"resources/a.md": file}, Changes: []crewsync.Change{{Path: "resources/a.md", After: &file, Chunks: chunk}, {Path: "resources/a.md", After: &file, Chunks: chunk}}},
	} {
		if err := validateCrewContent(&s); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
