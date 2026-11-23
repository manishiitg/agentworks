package claudecode

import "strings"

func isClaudeTranscriptSessionID(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) != 36 {
		return false
	}
	for i := 0; i < len(sessionID); i++ {
		ch := sessionID[i]
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
				return false
			}
		}
	}
	return true
}
