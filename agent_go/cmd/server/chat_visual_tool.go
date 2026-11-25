package server

import "github.com/manishiitg/coding-agent-loop/agent_go/internal/chatvisuals"

const chatVisualToolName = "author_chat_visual"

func registerChatVisualTool(reg definitionToolRegistrar) error {
	schema, err := chatvisuals.AuthorSchema()
	if err != nil {
		return err
	}
	return reg.RegisterCustomTool(chatVisualToolName,
		"Turn supplied data into one ready-to-paste chat visual. Provide exactly one of diagram, chart, timeline or cards, or use sequence for calls and replies or swimlane for ordered steps by role. On success copy the entire returned chat-ui block unchanged into your reply with the main facts in prose. If the fields cannot be corrected, give the prose answer.",
		schema, chatvisuals.Author, "presentation")
}
