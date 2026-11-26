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
