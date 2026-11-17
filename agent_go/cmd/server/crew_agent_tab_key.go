package server

import "strings"

// A Crew agent's chat key and its stable tab ID map to each other: Main is the
// project ID itself, other agents are project + side marker + tab.
func crewAgentTabKey(project, tab string) string {
	if tab == macMainTab {
		return project
	}
	return project + codeChatSideMarker + tab
}

func crewAgentKeyTab(key string) string {
	if _, tab, ok := strings.Cut(key, codeChatSideMarker); ok {
		if strings.HasPrefix(tab, sharedThreadChatPrefix) {
			suffix := strings.TrimPrefix(tab, sharedThreadChatPrefix)
			if at := strings.LastIndex(suffix, "-"); at > 0 && len(suffix[at+1:]) == 12 {
				tab = suffix[:at]
			}
		}
		if crewMacTabID(tab) {
			return tab
		}
		return ""
	}
	if key != "" && !strings.Contains(key, ":") {
		return macMainTab
	}
	return ""
}
