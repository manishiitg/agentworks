package v2

// Config holds configuration for creating a logger instance
type Config struct {
	// Level specifies the minimum log level (debug, info, warn, error)
	Level string

	// Format specifies the output format (text, json)
	Format string

	// Output specifies where to write logs
	// Options: "stdout", "stderr", or a file path
	Output string

	// EnableFile enables file logging
	// If true and FilePath is set, logs will be written to the file
	EnableFile bool

	// FilePath specifies the log file path
	// Only used if EnableFile is true
	FilePath string
}

// DefaultConfig returns a sensible default configuration
func DefaultConfig() Config {
	return Config{
		Level:      "info",
		Format:     "text",
		Output:     "stdout",
		EnableFile: false,
		FilePath:   "",
	}
}
