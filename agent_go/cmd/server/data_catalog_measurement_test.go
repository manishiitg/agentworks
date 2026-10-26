package server

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

// Observe the actual captured fixture at this registration stage. These are
// not declarations, universal token counts, or the final CLI MCP list (750-75).
func logDataCatalog(t *testing.T, surface string, source map[string]recordedTool) {
	t.Helper()
	projected := &recordingRegistrar{tools: map[string]recordedTool{}}
	for name, tool := range source {
		projected.tools[name] = tool
	}
	projectRecordedCatalog(t, projected)
	for _, stage := range []string{"source", "projected"} {
		tools := source
		if stage == "projected" {
			tools = projected.tools
		}
		for _, family := range []string{"files", "database"} {
			wanted := map[string]bool{family: true}
			for _, action := range platformtools.DataActions {
				if action.Family != family {
					continue
				}
				for _, name := range append([]string{action.Member, action.Source}, action.Aliases...) {
					if name != "" {
						wanted[name] = true
					}
				}
			}
			names := []string{}
			for name := range tools {
				if wanted[name] {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			envelope := []map[string]any{}
			for _, name := range names {
				tool := tools[name]
				envelope = append(envelope, map[string]any{"name": name, "description": tool.desc, "inputSchema": tool.params})
			}
			data, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("DATA_SCHEMA surface=%s stage=%s family=%s tools=%d bytes=%d names=%v", surface, stage, family, len(names), len(data), names)
		}
	}
}
