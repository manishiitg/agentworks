package cursorcli

import (
	"fmt"

	"strings"
)

const workbenchRealWorldTask = "You have three tools from the 'workbench' MCP server: web_search, write_file, read_file. " +
	"Do these steps in order, writing one short sentence of narration BEFORE each tool call:\n" +
	"1. Use web_search with the query \"project code word\" to find the code word.\n" +
	"2. Use write_file to save ONLY that code word into a file named exactly result.txt.\n" +
	"3. Use read_file on result.txt to confirm what was saved.\n" +
	"Finally, reply with the code word on its own line."

type cursorStreamCapture struct {
	content       strings.Builder
	contentTexts  []string
	contentChunks int
	toolStarts    int
	toolNames     []string
	order         []string
	terminalOnly  int
}

func assertCursorStreamNarration(capture cursorStreamCapture, markers []string) error {
	textIndex, toolIndex, allToolIndex := 0, 0, 0
	counts := make([]int, len(markers))
	for _, kind := range capture.order {
		switch kind {
		case "text":
			text := capture.contentTexts[textIndex]
			textIndex++
			for i, marker := range markers {
				if occurrences := strings.Count(text, marker); occurrences > 0 {
					counts[i] += occurrences
					if counts[i] != 1 {
						return fmt.Errorf("duplicate narration marker %s", marker)
					}
					if toolIndex != i {
						return fmt.Errorf("narration %s arrived at tool boundary %d, want %d", marker, toolIndex, i)
					}
				}
			}
		case "tool":
			// Cursor can discover dynamic MCP schemas before the three
			// requested operations. Discovery must not shift their boundaries.
			discovery := allToolIndex < len(capture.toolNames) && capture.toolNames[allToolIndex] == "GetDynamicTools"
			allToolIndex++
			if discovery {
				continue
			}
			if toolIndex < len(markers) && counts[toolIndex] != 1 {
				return fmt.Errorf("tool %d started without narration %s", toolIndex, markers[toolIndex])
			}
			toolIndex++
		}
	}
	if toolIndex != len(markers) {
		return fmt.Errorf("got %d tool starts, want %d", toolIndex, len(markers))
	}
	for i, n := range counts {
		if n != 1 {
			return fmt.Errorf("missing narration %s", markers[i])
		}
	}
	return nil
}
