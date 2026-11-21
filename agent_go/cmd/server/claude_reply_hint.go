package server

import (
	"regexp"
	"strconv"
)

// Claude 5.1 and later may show the person only a one-line summary of the text it writes before a tool call, so
// anything they must read goes in the final reply.
const claudeReplyHintInstruction = "Text you write before a tool call may reach the person only as a one-line summary. Put anything they must read in full in your final reply."

var claudeModelVersion = regexp.MustCompile(`claude-[a-z]+-(\d+)-(\d+)`)

// claudeSummarisesPreToolText reports whether a Claude model id (claude-opus-5-5) is version 5.1 or later.
func claudeSummarisesPreToolText(modelID string) bool {
	m := claudeModelVersion.FindStringSubmatch(modelID)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 5 || (major == 5 && minor >= 1)
}
