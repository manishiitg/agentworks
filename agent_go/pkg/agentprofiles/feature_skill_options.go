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
		if copy.Name == FeatureSkillName(profile.ID, "work-schedules-and-bots") && FeatureOption(profile, "bots", "dm_only") == "true" {
			body := copy.Content
			if start := strings.Index(body, "## Project-chat bots"); start >= 0 {
				body = body[:start]
			}
			body += "## Direct-message bots\n\nSlack supports direct messages only, with one chat per person. Channels, group chats, agent Slack sends and channel-route tools are unavailable. WhatsApp is for the owner. Configure this project's bot in its Bots settings; never claim a setup succeeded without verification.\n"
			copy.Description = "Manage this project's message schedules, webhook triggers, and direct-message bots. Read before recurring work, event-driven messages, or bot setup."
			if FeatureOption(profile, "bots", "gmail") == "own" {
				start := strings.Index(copy.Content, "## Gmail and Google Workspace")
				if start >= 0 {
					google := copy.Content[start:]
					if end := strings.Index(google[3:], "\n## "); end >= 0 {
						google = google[:end+3]
					}
					body += "\n" + google
				}
				// Code's private-Gmail variant also retains incoming-email procedures;
				// clipping channel/Slack guidance must not clip its admitted setup tools.
				if start := strings.Index(copy.Content, "## Incoming Gmail triggers:"); start >= 0 {
					body += "\n" + copy.Content[start:]
				}
				body += "\nUse only this project's private Google accounts in its owner's chats. Reads require an observed read grant; drafting/sending requires agent-write opt-in plus compose grant.\n"
				copy.Description += " Also read before using this project's private Google/Gmail accounts."
			} else {
				body += "\nGmail and Google Workspace are unavailable in this session.\n"
			}
			copy.Content = body
		}
		if copy.Name == FeatureSkillName(profile.ID, "work-workflow-files") {
			switch FeatureOption(profile, "workflow-references", "direction") {
			case "outbound":
				if start := strings.Index(copy.Content, "- **Offer**"); start >= 0 {
					if end := strings.Index(copy.Content[start:], "Calls that would loop"); end >= 0 {
						copy.Content = copy.Content[:start] + copy.Content[start+end:]
					}
				}
				copy.Content += "\n## Outbound-only session\n\nCall only accessible Crews and workflows. This workspace cannot define or answer functions; private workspaces are not valid targets.\n"
			}
		}
		result = append(result, &copy)
	}
	return result
}
