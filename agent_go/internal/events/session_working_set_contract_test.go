package events

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestFrontendChildTranscriptRetentionExtendsBackendWireSuppression pins the
// intentional relationship between two different policies:
//
//   - Go omits durable child transcript detail from the session wire payload.
//   - TypeScript additionally refuses to retain the three live streaming events
//     that must still cross SSE for ephemeral terminal/progress rendering.
//
// Equality would be a bug. The frontend set must be the backend set plus exactly
// streaming_start, streaming_chunk, and streaming_end.
func TestFrontendChildTranscriptRetentionExtendsBackendWireSuppression(t *testing.T) {
	frontendTypes := readFrontendChildTranscriptDetailTypes(t)
	backendTypes := make(map[string]bool, len(childTranscriptDetailEventTypes))
	for eventType := range childTranscriptDetailEventTypes {
		backendTypes[eventType] = true
	}

	missingFromFrontend := eventTypeSetDifference(backendTypes, frontendTypes)
	if len(missingFromFrontend) != 0 {
		t.Fatalf(
			"frontend child-retention policy is missing backend-suppressed event types %v; this silently restores session-wide retention",
			missingFromFrontend,
		)
	}

	wantFrontendOnly := map[string]bool{
		"streaming_start": true,
		"streaming_chunk": true,
		"streaming_end":   true,
	}
	gotFrontendOnly := eventTypeSetDifference(frontendTypes, backendTypes)
	if missing := eventTypeSetDifference(wantFrontendOnly, sliceToEventTypeSet(gotFrontendOnly)); len(missing) != 0 {
		t.Fatalf("frontend-only streaming policy is missing %v; child streaming snapshots would be retained in Zustand", missing)
	}
	if unexpected := eventTypeSetDifference(sliceToEventTypeSet(gotFrontendOnly), wantFrontendOnly); len(unexpected) != 0 {
		t.Fatalf("frontend-only retention policy has unexpected event types %v; decide whether they also belong in backend wire suppression", unexpected)
	}
}

func readFrontendChildTranscriptDetailTypes(t *testing.T) map[string]bool {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test path")
	}
	// 11ab6120b ("One session-event client, shared by AgentWorks and
	// ExampleApp") moved the real policy to frontend/shared/session/
	// workingSet.ts; frontend/src/utils/sessionEventWorkingSet.ts is now
	// just a re-export shim with no declaration for this scraper to find.
	path := filepath.Clean(filepath.Join(
		filepath.Dir(currentFile),
		"../../../frontend/shared/session/workingSet.ts",
	))
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frontend session working-set policy %s: %v", path, err)
	}

	const marker = "const CHILD_TRANSCRIPT_DETAIL_EVENT_TYPES"
	source := string(sourceBytes)
	markerIndex := strings.Index(source, marker)
	if markerIndex < 0 {
		t.Fatalf("frontend policy declaration %q not found in %s", marker, path)
	}
	declaration := source[markerIndex:]
	listStart := strings.Index(declaration, "[")
	if listStart < 0 {
		t.Fatalf("frontend policy declaration %q has no list", marker)
	}
	declaration = declaration[listStart+1:]
	listEnd := strings.Index(declaration, "])")
	if listEnd < 0 {
		t.Fatalf("frontend policy declaration %q has no closing list", marker)
	}

	quotedString := regexp.MustCompile(`["']([^"']+)["']`)
	matches := quotedString.FindAllStringSubmatch(declaration[:listEnd], -1)
	if len(matches) == 0 {
		t.Fatalf("frontend policy declaration %q contains no event types", marker)
	}
	types := make(map[string]bool, len(matches))
	for _, match := range matches {
		types[match[1]] = true
	}
	return types
}

func eventTypeSetDifference(left, right map[string]bool) []string {
	difference := make([]string, 0)
	for eventType := range left {
		if !right[eventType] {
			difference = append(difference, eventType)
		}
	}
	sort.Strings(difference)
	return difference
}

func sliceToEventTypeSet(types []string) map[string]bool {
	set := make(map[string]bool, len(types))
	for _, eventType := range types {
		set[eventType] = true
	}
	return set
}
