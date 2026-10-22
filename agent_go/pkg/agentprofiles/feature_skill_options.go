package agentprofiles

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// FeatureSkillsForSession renders option-specific procedures without mutating
// the global builtin skill. Rebuild from the current resolved profile each turn.
func FeatureSkillsForSession(profile Profile, attached []*llmtypes.Skill) []*llmtypes.Skill {
	result := make([]*llmtypes.Skill, 0, len(attached))
	for _, skill := range attached {
		if skill == nil {
			continue
		}
		copy := *skill
		if FeatureOption(profile, "bots", "dm_only") == "true" {
			if copy.Name == "panel-slack-bot" {
				copy.Content = "# Direct-message bots\n\nSlack supports direct messages only, with one chat per person. Channels, group chats, agent Slack sends and channel-route tools are unavailable. Configure this project's bot in its Bots settings and verify the result.\n"
				copy.SupportingFiles = nil
			}
			if copy.Name == "panel-google-apps" {
				if FeatureOption(profile, "bots", "gmail") == "own" {
					copy.Content += "\nUse only this project's private Google accounts in its owner's chats. Reads require an observed read grant; drafting/sending requires agent-write opt-in plus compose grant.\n"
				} else {
					copy.Content = "Gmail and Google Workspace are unavailable in this session."
					copy.SupportingFiles = nil
				}
			}
		}
		if copy.Name == "panel-functions" {
			switch FeatureOption(profile, "workflow-references", "direction") {
			case "outbound":
				copy.SupportingFiles = append([]llmtypes.SkillFile(nil), copy.SupportingFiles...)
				for i, file := range copy.SupportingFiles {
					body := string(file.Content)
					if start := strings.Index(body, "- **Offer**"); start >= 0 {
						if end := strings.Index(body[start:], "Calls that would loop"); end >= 0 {
							body = body[:start] + body[start+end:]
						}
					}
					copy.SupportingFiles[i].Content = []byte(body)
				}
				copy.Content += "\n## Outbound-only session\n\nCall only accessible Crews and workflows. This workspace cannot define or answer functions; private workspaces are not valid targets.\n"
			}
		}
		result = append(result, &copy)
	}
	return result
}
