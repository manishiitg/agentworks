package platformtools

import "regexp"

// groupPurpose is the first line of each grouped tool's description. search_tools shows only the first 350
// characters, so this line is what the agent picks a group by. The old generated "Use this project's X actions."
// told it nothing, and agents opened 1.7 specs per call they made (test server, Oct-Nov 2026).
var groupPurpose = map[string]string{
	"account":      "People and accounts, for administrators: user access, availability, usage and shared-account limits.",
	"automations":  "Make this project's work start by itself: at set times (scope=schedule), when a condition becomes true (scope=watcher), or when another service calls a web address (scope=webhook); list, change and check their runs.",
	"brain":        "Company shared files (Brain): browse, read, search and save shared folders and files, and publish company skills.",
	"browser":      "Use a web browser: open pages and act on them, and set up or choose a browser connection.",
	"costs":        "Token usage and cost of this project: totals and breakdowns by person, model and activity.",
	"dashboard":    "Build, check, preview and publish this project's dashboard (an HTML page with its data).",
	"database":     "This project's database: query it, change data, run migrations and take snapshots; also read a linked Goal's database.",
	"files":        "This project's files: list, search, read, write and patch them, attach shared folders, and make links to files.",
	"functions":    "Functions: define this project's functions, call another Crew's or workflow's functions, and check a call in progress.",
	"help":         "AgentWorks help: how-to topics, the current context and slash command guidance.",
	"integrations": "Google and Slack connections: connect accounts, grant access, and set up the Slack bot and its routes.",
	"mcp":          "MCP servers (outside apps and services) this project can use: find, add, connect and manage them.",
	"media":        "Generate or edit images, documents and video.",
	"messages":     "Messages and email: send to another agent, Slack or email, read the agent mailbox and inbox, and set up channels.",
	"metrics":      "Record and read this project's metrics (numbers tracked over time).",
	"models":       "Which AI model this project uses: list, test and change it.",
	"needs_you":    "Decisions waiting for a person (the Needs you list): create, list, answer or dismiss them.",
	"relay":        "Relays: run, test and publish a relay, and read its runs and releases.",
	"runs":         "Goal workflow runs: start a run or step, check status and logs, answer a run's question, or stop it.",
	"sessions":     "Chats and background work: list, open and read this project's chats, and start or check background tasks.",
	"settings":     "Project settings: read and change them, choose which secrets are used, and manage variables.",
	"share":        "Who can use this project: only me, chosen people or everyone.",
	"skills":       "Skills (reusable instructions): find, install and remove them for this project, and manage its slash commands.",
	"terminal":     "Run shell commands on the server or on the person's laptop, and read, write or copy files there.",
	"ui":           "Show or reload the project dashboard in the side pane.",
	"vault":        "Secrets and sign-ins: store secret values, choose which secrets this project uses, and connect the person's own apps.",
	"workspace":    "The project itself: its references to other projects, owners, name, copy or delete, access and the agent's identity.",
}

// fillerActionDescription matches generated action text that only repeats the action's name.
var fillerActionDescription = regexp.MustCompile(`^Use (this project[’']s|the current) .*(action|operation)( with its current access)?\.$`)
