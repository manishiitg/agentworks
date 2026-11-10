package server

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"sync"
)

// Cache only file contents, never Team membership, folder selection or access.
// A small bounded cache avoids retaining every inactive project's history.
var crewTeamFileStates = struct {
	sync.Mutex
	entries map[string]crewTeamFileStateEntry
}{entries: make(map[string]crewTeamFileStateEntry)}

type crewTeamFileStateEntry struct {
	info  os.FileInfo
	state crewTeamFileState
}

// Returned state is immutable inside the cache; readCrewTeamFileState gives
// each caller an independent copy, preserving its existing editing contract.
func cachedCrewTeamFileState(path string) (crewTeamFileState, error) {
	state := crewTeamFileState{Versions: map[string]crewTeamVersion{}, Receipts: map[string]crewTeamReceipt{}}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	crewTeamFileStates.Lock()
	defer crewTeamFileStates.Unlock()
	if cached, ok := crewTeamFileStates.entries[path]; ok && os.SameFile(cached.info, info) && cached.info.Size() == info.Size() && cached.info.ModTime().Equal(info.ModTime()) {
		return cached.state, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Versions == nil {
		state.Versions = map[string]crewTeamVersion{}
	}
	if state.Receipts == nil {
		state.Receipts = map[string]crewTeamReceipt{}
	}
	if _, exists := crewTeamFileStates.entries[path]; !exists && len(crewTeamFileStates.entries) >= 16 {
		for old := range crewTeamFileStates.entries {
			delete(crewTeamFileStates.entries, old)
			break
		}
	}
	crewTeamFileStates.entries[path] = crewTeamFileStateEntry{info, state}
	return state, nil
}

func cloneCrewTeamFileState(state crewTeamFileState) crewTeamFileState {
	out := state
	out.Versions = maps.Clone(state.Versions)
	for path, version := range out.Versions {
		if version.File != nil {
			file := *version.File
			version.File = &file
		}
		version.Chunks = slices.Clone(version.Chunks)
		out.Versions[path] = version
	}
	out.Events = slices.Clone(state.Events)
	for i := range out.Events {
		if out.Events[i].File != nil {
			file := *out.Events[i].File
			out.Events[i].File = &file
		}
		out.Events[i].Chunks = slices.Clone(out.Events[i].Chunks)
	}
	out.Clashes = slices.Clone(state.Clashes)
	out.Receipts = maps.Clone(state.Receipts)
	for key, receipt := range out.Receipts {
		receipt.Conflicts = slices.Clone(receipt.Conflicts)
		out.Receipts[key] = receipt
	}
	out.OnPurpose = maps.Clone(state.OnPurpose)
	return out
}
