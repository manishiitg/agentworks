package commands

import (
	"fmt"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// DiscoverCommands discovers all user-defined commands in the workspace
func DiscoverCommands(workspaceAPIURL string) ([]Command, error) {
	return DiscoverCommandsAt(workspaceAPIURL, CustomCommandsSubPath)
}

// DiscoverCommandsAt discovers commands below a caller-resolved scope. The
// server resolves that scope from the signed-in user/current workspace; command
// storage itself never accepts a free-form root from the browser or model.
func DiscoverCommandsAt(workspaceAPIURL, commandsPath string) ([]Command, error) {
	client := skills.NewWorkspaceAPIClient(workspaceAPIURL)

	// List all folders in commands/custom/
	entries, err := client.ListFiles(commandsPath)
	if err != nil {
		// If folder doesn't exist, return empty list
		return []Command{}, nil
	}

	var cmds []Command

	for _, entry := range entries {
		if entry.Type != "folder" {
			continue
		}

		folderName := path.Base(entry.Filepath)
		cmdFilePath := path.Join(entry.Filepath, CommandFileName)

		content, err := client.ReadFile(cmdFilePath)
		if err != nil {
			// Skip folders without COMMAND.md
			continue
		}

		cmd, err := ParseCommandFromContent(content, folderName, cmdFilePath)
		if err != nil {
			// Skip invalid commands
			continue
		}

		cmds = append(cmds, *cmd)
	}

	return cmds, nil
}

// GetCommand retrieves a specific command by folder name
func GetCommand(workspaceAPIURL, folderName string) (*Command, error) {
	return GetCommandAt(workspaceAPIURL, CustomCommandsSubPath, folderName)
}

func GetCommandAt(workspaceAPIURL, commandsPath, folderName string) (*Command, error) {
	if err := validateFolderName(folderName); err != nil {
		return nil, err
	}
	client := skills.NewWorkspaceAPIClient(workspaceAPIURL)

	cmdFilePath := path.Join(commandsPath, folderName, CommandFileName)
	content, err := client.ReadFile(cmdFilePath)
	if err != nil {
		return nil, fmt.Errorf("command not found: %w", err)
	}

	cmd, err := ParseCommandFromContent(content, folderName, cmdFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse command: %w", err)
	}

	return cmd, nil
}

// CreateCommand creates a new command folder and writes COMMAND.md
func CreateCommand(workspaceAPIURL, folderName, content string) (*Command, error) {
	return CreateCommandAt(workspaceAPIURL, CustomCommandsSubPath, folderName, content)
}

func CreateCommandAt(workspaceAPIURL, commandsPath, folderName, content string) (*Command, error) {
	if err := validateFolderName(folderName); err != nil {
		return nil, err
	}
	content = EnsureContextPlaceholder(content)
	// Validate content first
	frontmatter, body, err := ValidateCommandContent(content)
	if err != nil {
		return nil, fmt.Errorf("invalid command content: %w", err)
	}

	client := skills.NewWorkspaceAPIClient(workspaceAPIURL)

	// Create the folder
	folderPath := path.Join(commandsPath, folderName)
	if _, err := GetCommandAt(workspaceAPIURL, commandsPath, folderName); err == nil {
		return nil, fmt.Errorf("command %q already exists", folderName)
	}
	if err := client.CreateFolder(folderPath); err != nil {
		return nil, fmt.Errorf("failed to create command folder: %w", err)
	}

	// Write COMMAND.md
	cmdFilePath := path.Join(folderPath, CommandFileName)
	if err := client.WriteFile(cmdFilePath, content); err != nil {
		return nil, fmt.Errorf("failed to write command file: %w", err)
	}

	return &Command{
		Frontmatter: *frontmatter,
		Content:     body,
		FolderName:  folderName,
		FilePath:    cmdFilePath,
	}, nil
}

// UpdateCommand updates a command's COMMAND.md content
func UpdateCommand(workspaceAPIURL, folderName, content string) (*Command, error) {
	return UpdateCommandAt(workspaceAPIURL, CustomCommandsSubPath, folderName, content)
}

func UpdateCommandAt(workspaceAPIURL, commandsPath, folderName, content string) (*Command, error) {
	if err := validateFolderName(folderName); err != nil {
		return nil, err
	}
	content = EnsureContextPlaceholder(content)
	frontmatter, body, err := ValidateCommandContent(content)
	if err != nil {
		return nil, fmt.Errorf("invalid command content: %w", err)
	}

	client := skills.NewWorkspaceAPIClient(workspaceAPIURL)

	cmdFilePath := path.Join(commandsPath, folderName, CommandFileName)
	if err := client.WriteFile(cmdFilePath, content); err != nil {
		return nil, fmt.Errorf("failed to write command: %w", err)
	}

	return &Command{
		Frontmatter: *frontmatter,
		Content:     body,
		FolderName:  folderName,
		FilePath:    cmdFilePath,
	}, nil
}

// DeleteCommand deletes a command folder
func DeleteCommand(workspaceAPIURL, folderName string) error {
	return DeleteCommandAt(workspaceAPIURL, CustomCommandsSubPath, folderName)
}

func DeleteCommandAt(workspaceAPIURL, commandsPath, folderName string) error {
	if err := validateFolderName(folderName); err != nil {
		return err
	}
	client := skills.NewWorkspaceAPIClient(workspaceAPIURL)

	folderPath := path.Join(commandsPath, folderName)
	if err := client.DeleteFolder(folderPath); err != nil {
		return fmt.Errorf("failed to delete command: %w", err)
	}

	return nil
}

// ContextPlaceholder is replaced with the text the user typed alongside the
// slash command.
const ContextPlaceholder = "{{context}}"

// EnsureContextPlaceholder appends {{context}} on its own line to a command
// that does not use it, so text typed with the command reaches the agent
// instead of being dropped. On its own line it renders to nothing when the
// user types nothing.
func EnsureContextPlaceholder(content string) string {
	if strings.Contains(content, ContextPlaceholder) {
		return content
	}
	return strings.TrimRight(content, " \t\r\n") + "\n\n" + ContextPlaceholder + "\n"
}

func validateFolderName(folderName string) error {
	folderName = strings.TrimSpace(folderName)
	if folderName == "" || folderName == "." || folderName == ".." || path.Base(folderName) != folderName {
		return fmt.Errorf("invalid command folder name")
	}
	for _, c := range folderName {
		if !isValidNameChar(c) {
			return fmt.Errorf("command folder name contains invalid character %q", c)
		}
	}
	return nil
}

// ValidateCommandContent validates the complete COMMAND.md content
func ValidateCommandContent(content string) (*CommandFrontmatter, string, error) {
	frontmatter, body, err := ParseCommandFile(content)
	if err != nil {
		return nil, "", err
	}

	if strings.TrimSpace(frontmatter.Name) == "" {
		return nil, "", fmt.Errorf("name is required")
	}

	if strings.TrimSpace(frontmatter.Description) == "" {
		return nil, "", fmt.Errorf("description is required")
	}

	// Validate name format
	for _, c := range frontmatter.Name {
		if !isValidNameChar(c) {
			return nil, "", fmt.Errorf("name contains invalid character '%c' (only alphanumeric, hyphens, and underscores allowed)", c)
		}
	}

	return frontmatter, body, nil
}

func isValidNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_'
}

// Every Crew starts with one flagged command. It is a normal command: it can
// be renamed or removed. Crew creation and the one-off deploy migration add it
// (alongside CardDefaultMarker, so a deleted default is never added again);
// listing never writes.
const (
	DefaultCardCommandName    = "check-this-change"
	DefaultCardCommandMessage = "Look over the files you changed in this reply. Point out anything that looks wrong or risky, anything you missed, and what I should test."
	CardDefaultMarker         = ".card-default-seeded"
)

// DefaultCardCommandContent is the COMMAND.md text of the default card command.
func DefaultCardCommandContent() string {
	fm := &CommandFrontmatter{Name: DefaultCardCommandName, Description: "Check this change", Icon: "eye", Modes: []string{"multi-agent"}, ShowOnCard: true}
	content, _ := SerializeCommandFile(fm, DefaultCardCommandMessage)
	return EnsureContextPlaceholder(content)
}
