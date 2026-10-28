package commands

// CommandFrontmatter represents the YAML frontmatter in COMMAND.md
type CommandFrontmatter struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Icon        string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Modes       []string `yaml:"modes,omitempty" json:"modes,omitempty"`
	// ShowOnCard makes the command a button on each reply card. Default off.
	ShowOnCard bool `yaml:"show_on_card,omitempty" json:"show_on_card"`
}

// Command represents a complete command with parsed content
type Command struct {
	Frontmatter CommandFrontmatter `json:"frontmatter"`
	Content     string             `json:"content"`     // Prompt template after frontmatter
	FolderName  string             `json:"folder_name"` // Command folder name
	FilePath    string             `json:"file_path"`   // Relative path in workspace
}

// CreateCommandRequest represents a request to create a command
type CreateCommandRequest struct {
	Name    string `json:"name"`    // Folder name
	Content string `json:"content"` // Full COMMAND.md content (frontmatter + body)
}

// UpdateCommandRequest represents a request to update a command
type UpdateCommandRequest struct {
	Content string `json:"content"` // Full COMMAND.md content (frontmatter + body)
}

// ListCommandsResponse represents the response from listing commands
type ListCommandsResponse struct {
	Commands []Command `json:"commands"`
	Total    int       `json:"total"`
}

// CommandsBasePath is the base path for commands in the workspace
const CommandsBasePath = "commands"

// CustomCommandsSubPath is the path for user-created commands
const CustomCommandsSubPath = "commands/custom"

// CommandFileName is the required file name for command definitions
const CommandFileName = "COMMAND.md"
