package filesnapshots

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
)

// CardAction is one action on a reply's file card. The server decides the
// whole ordered list for the viewer; the card draws each entry where its
// placement says: "row" is an icon button with a tooltip, "more" is the ⋯
// menu, "pr_line" is a link on its group's pull request line. Phones move the
// person's own buttons and Add button from the row into More.
type CardAction struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // builtin | git_step | fix | command
	// Label is the button name; AriaLabel is set only when the spoken name differs.
	Label       string `json:"label"`
	AriaLabel   string `json:"aria_label,omitempty"`
	Description string `json:"description,omitempty"` // the tooltip line
	// Icon is "octicon:<id>" or a Lucide icon name.
	Icon      string `json:"icon,omitempty"`
	Placement string `json:"placement"` // row | more | pr_line
	// Prompt is the exact text sent to the chat's agent. Review changes has
	// none (it opens Review); Add button puts its prompt in the composer.
	Prompt      string `json:"prompt,omitempty"`
	Group       string `json:"group,omitempty"` // root of the repo group it applies to
	EditorsOnly bool   `json:"editors_only,omitempty"`
}

const (
	CardReview    = "review"
	CardAddButton = "add_button"
	// The row keeps two of the person's card buttons; the rest go under More.
	cardRowCommands = 2
)

var nextStepLabels = map[string]string{"share_pr": "Share as a pull request", "update_pr": "Update the pull request", "get_latest": "Get the latest changes"}

// CardActions is the card's action list. Everyone gets Review changes; only
// editors of the Crew get the Git actions, fix prompts and their own card
// commands (show_on_card). groups are the card's groups with pull request
// status and next step when they were looked up.
func CardActions(groups []Group, canEdit bool, cardCommands []commands.Command) []CardAction {
	out := []CardAction{{ID: CardReview, Kind: "builtin", Label: "Review changes", Description: "See every file this reply changed, line by line.", Icon: "octicon:file-diff", Placement: "row"}}
	if !canEdit {
		return out
	}
	var repos []Group
	for _, g := range groups {
		if g.Kind == "repo" && g.Branch != "" {
			repos = append(repos, g)
		}
	}
	if len(repos) > 0 {
		branch := "their current branches"
		if len(repos) == 1 {
			branch = repos[0].Branch
		}
		commit, push := make([]string, len(repos)), make([]string, len(repos))
		for i, g := range repos {
			commit[i], push[i] = commitPrompt(g), pushPrompt(g)
		}
		out = append(out,
			CardAction{ID: "commit", Kind: "builtin", Label: "Commit on the current branch", Description: "Ask the agent to commit these files on " + branch + ". Nothing is pushed.", Icon: "octicon:git-commit", Placement: "row", Prompt: strings.Join(commit, "\n\n"), EditorsOnly: true},
			CardAction{ID: "push", Kind: "builtin", Label: "Push to a branch and open a PR", Description: "Ask the agent to push a branch and open a pull request with a full description.", Icon: "octicon:git-pull-request", Placement: "row", Prompt: strings.Join(push, "\n\n"), EditorsOnly: true})
	}
	// Menu entries name their checkout when the card has more than one.
	suffix := func(g Group) string {
		if len(repos) > 1 {
			return " · " + g.Name
		}
		return ""
	}
	for _, g := range repos {
		label, ok := nextStepLabels[g.NextStep]
		if !ok {
			continue
		}
		icon, prompt := "octicon:git-pull-request", pushPrompt(g)
		switch g.NextStep {
		case "update_pr":
			prompt = updatePRPrompt(g)
		case "get_latest":
			icon, prompt = "arrow-down-to-line", getLatestPrompt(g)
		}
		out = append(out, CardAction{ID: "next:" + g.Root, Kind: "git_step", Label: label + suffix(g), Icon: icon, Placement: "more", Prompt: prompt, Group: g.Root, EditorsOnly: true})
	}
	var links []CardAction
	for _, g := range repos {
		for _, fix := range fixActions(g) {
			links = append(links, CardAction{ID: fix.ID + ":link", Kind: "fix", Label: fix.Label, Icon: fix.Icon, Placement: "pr_line", Prompt: fix.Prompt, Group: g.Root, EditorsOnly: true})
			fix.Label += suffix(g)
			out = append(out, fix)
		}
	}
	shown := 0
	for _, command := range cardCommands {
		fm := command.Frontmatter
		if !fm.ShowOnCard {
			continue
		}
		placement := "more"
		if shown < cardRowCommands {
			placement, shown = "row", shown+1
		}
		description, aria := fm.Description, ""
		if description == "" {
			description = "Runs your " + fm.Name + " command in this chat."
		} else if description != fm.Name {
			aria = fm.Description
		}
		out = append(out, CardAction{ID: "command:" + fm.Name, Kind: "command", Label: fm.Name, AriaLabel: aria, Description: description, Icon: fm.Icon, Placement: placement, Prompt: commandPrompt(command.Content, cardContext(groups)), EditorsOnly: true})
	}
	out = append(out, CardAction{ID: CardAddButton, Kind: "builtin", Label: "Add button", Description: "Save a reusable button on this Crew's file-change toolbar.", Icon: "plus", Placement: "row", Prompt: "Add a reusable button to this Crew's AgentWorks \"Files changed during this reply\" toolbar.\n\nButton name: ___\nMessage to send when clicked: ___\n\nRead the crew-builder skill's reply-card button guidance. Save this Crew command with manage_custom_commands and show_on_card=true, using a fitting icon.", EditorsOnly: true})
	return append(out, links...)
}

// fixActions offers only what GitHub reports for an open pull request.
func fixActions(g Group) []CardAction {
	pr := g.PR
	if pr == nil || pr.Number == 0 || pr.State != "open" {
		return nil
	}
	var out []CardAction
	add := func(id, label, prompt string) {
		out = append(out, CardAction{ID: id + ":" + g.Root, Kind: "fix", Label: label, Icon: "wrench", Placement: "more", Prompt: prompt, Group: g.Root, EditorsOnly: true})
	}
	if pr.Checks == "failing" {
		names := ""
		if len(pr.FailingChecks) > 0 {
			names = ": " + strings.Join(pr.FailingChecks, ", ")
		}
		add("fix-checks", "Fix failing checks", fmt.Sprintf("%s has failing checks%s. Open the failing runs, fix the cause on branch %s, push, and tell me what you changed.", prName(g), names, g.Branch))
	}
	if pr.Review == "changes_requested" {
		add("fix-review", "Address review comments", fmt.Sprintf("%s has review comments asking for changes. Read every review comment, make the requested changes on branch %s, push, and tell me what you changed for each comment.", prName(g), g.Branch))
	}
	if pr.Conflicts {
		add("fix-conflicts", "Resolve conflicts", fmt.Sprintf("%s cannot merge because of conflicts with its base branch. Bring branch %s up to date with the base branch, resolve the conflicts keeping what both sides meant, push, and tell me which files had conflicts and how you resolved them.", prName(g), g.Branch))
	}
	return out
}

// The requests below go to the agent in this same chat. Only this reply's
// files are named, and the agent is told never to push the main branch.
func commitPrompt(g Group) string {
	return fmt.Sprintf("Commit these files in %s (%s) on branch %s. Stage only the files listed here, by name, and use a short message that describes the change. Do not push yet.\n%s", g.Name, groupFolder(g), g.Branch, fileLines(g))
}

func pushPrompt(g Group) string {
	return fmt.Sprintf("Open a pull request for this reply's work in %s (%s). If branch %s is the default branch (main or master), first create a new branch from it with a short descriptive name; otherwise use %s. Never push the default branch. If these files are not committed yet, commit them, staging only the files listed here, by name:\n%s\nThen push the branch to GitHub and open a pull request with a proper description: a short title, a summary of what changed and why based on this chat, the list of changed files, and how it was tested. Reply with the pull request link.", g.Name, groupFolder(g), g.Branch, g.Branch, fileLines(g))
}

func updatePRPrompt(g Group) string {
	return fmt.Sprintf("Update %s with this reply's work on branch %s. If these files are not committed yet, commit them, staging only the files listed here, by name:\n%s\nThen push branch %s, update the pull request description if what it does has changed, and reply with the pull request link.", prName(g), g.Branch, fileLines(g), g.Branch)
}

func getLatestPrompt(g Group) string {
	return fmt.Sprintf("Branch %s in %s (%s) is behind its copy on GitHub. Bring in the latest changes without losing any work here: keep uncommitted changes safe first, resolve any conflicts, then tell me what came in.", g.Branch, repoName(g), groupFolder(g))
}

func groupFolder(g Group) string {
	if g.Root == "" {
		return "the Crew folder"
	}
	return g.Root
}

func repoName(g Group) string {
	if g.GitHubRepo != "" {
		return g.GitHubRepo
	}
	return g.Name
}

func prName(g Group) string {
	number := 0
	if g.PR != nil {
		number = g.PR.Number
	}
	return fmt.Sprintf("PR #%d in %s", number, repoName(g))
}

func fileLines(g Group) string {
	lines := make([]string, len(g.Files))
	for i, file := range g.Files {
		lines[i] = "- " + file.Path
	}
	text := strings.Join(lines, "\n")
	if g.MoreCount > 0 {
		text += fmt.Sprintf("\n(and %d more files from this reply)", g.MoreCount)
	}
	return text
}

// cardContext is this reply's context for a card command: it replaces
// {{context}} in the command's prompt.
func cardContext(groups []Group) string {
	var paths []string
	var repo *Group
	for i, g := range groups {
		for _, file := range g.Files {
			paths = append(paths, file.Path)
		}
		if repo == nil && g.Kind == "repo" && g.Branch != "" {
			repo = &groups[i]
		}
	}
	if len(paths) > 20 {
		paths = paths[:20]
	}
	text := "Context: this reply changed these files: " + strings.Join(paths, ", ")
	if repo != nil {
		text += " in " + repo.Name + " on branch " + repo.Branch
		if repo.PR != nil && repo.PR.URL != "" {
			text += "; PR " + repo.PR.URL
		}
	}
	return text
}

// commandPrompt fills a command's {{context}}; a command without the
// placeholder gets the context after its prompt.
func commandPrompt(template, context string) string {
	if !strings.Contains(template, commands.ContextPlaceholder) {
		return strings.TrimSpace(template) + "\n\n" + context
	}
	return strings.TrimSpace(strings.ReplaceAll(template, commands.ContextPlaceholder, context))
}
