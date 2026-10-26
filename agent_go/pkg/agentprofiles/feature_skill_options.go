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
		if FeatureOption(profile, "bots", "dm_only") == "true" && copy.Name == "panel-channels" {
			copy.SupportingFiles = append([]llmtypes.SkillFile(nil), copy.SupportingFiles...)
			for i, file := range copy.SupportingFiles {
				if strings.HasPrefix(file.RelPath, "references/slack-bot/") {
					copy.SupportingFiles[i].Content = []byte("# Direct-message bots\n\nSlack supports direct messages only, with one chat per person. Channels, group chats, agent Slack sends and channel-route tools are unavailable. Configure this project's bot in its Bots settings and verify the result.\n")
				}
				if strings.HasPrefix(file.RelPath, "references/google-apps/") {
					if FeatureOption(profile, "bots", "gmail") == "own" {
						copy.SupportingFiles[i].Content = append(append([]byte(nil), file.Content...), []byte("\nUse only this project's private Google accounts in its owner's chats. Reads require an observed read grant; drafting/sending requires agent-write opt-in plus compose grant.\n")...)
					} else {
						copy.SupportingFiles[i].Content = []byte("Gmail and Google Workspace are unavailable in this session.")
					}
				}
			}
			copy.Content += "\nThis session supports direct-message bots only. Google availability and its own-account restriction are described in the Google procedure.\n"
		}
		if copy.Name == "panel-dashboard" {
			switch FeatureOption(profile, "workflow-references", "direction") {
			case "outbound":
				copy.SupportingFiles = append([]llmtypes.SkillFile(nil), copy.SupportingFiles...)
				for i, file := range copy.SupportingFiles {
					body := string(file.Content)
					if !strings.HasPrefix(file.RelPath, "references/functions/") {
						continue
					}
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
