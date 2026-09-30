package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/mcp"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/testing"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "mcp-agent",
	Short: "MCP Agent for multi-server tool coordination",
	Long: `A powerful MCP agent that coordinates multiple MCP servers to handle complex tasks.
	
This tool provides:
- Multi-server MCP coordination
- Tool discovery and management
- Comprehensive tracing and observability
- Event-driven architecture`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Check for log file flags immediately and disable console output first
		logFile, _ := cmd.Flags().GetString("log-file")

		// Also check for test.log-file flag (used by test commands)
		if logFile == "" {
			logFile = viper.GetString("test.log-file")
		}

		// Set environment variables for immediate effect when log file is specified
		if logFile != "" {
			os.Setenv("LOG_FILE", logFile)
		}

		if logFile != "" {
			// Create log directory if it doesn't exist
			logDir := filepath.Dir(logFile)
			if err := os.MkdirAll(logDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create log directory: %v\n", err)
				os.Exit(1)
			}

			// REMOVED: Global stdout/stderr redirection
			// This was causing conflicts with individual command logging
			// Individual commands should handle their own logging configuration

			// REMOVED: Global logger initialization
			// This was causing conflicts with individual command logging
			// Individual commands should handle their own logging configuration
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// A deployment may point TMPDIR at a private folder so launch scripts,
	// per-launch CLI configs and bridge tokens stay out of the shared /tmp
	// (coding CLIs can write /tmp). Make sure it exists before anything
	// creates a temp file.
	if dir := os.Getenv("TMPDIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.mcp-agent.yaml)")
	rootCmd.PersistentFlags().String("trace-provider", "console", "tracing provider (console, langfuse, noop)")
	rootCmd.PersistentFlags().String("langfuse-host", "https://cloud.langfuse.com", "Langfuse host URL")
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug logging")
	rootCmd.PersistentFlags().Int("max-turns", 500, "maximum conversation turns per agent")
	rootCmd.PersistentFlags().Float64("temperature", 0.0, "LLM temperature")

	// Logging flags
	rootCmd.PersistentFlags().String("log-file", "", "log file path (optional)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error, fatal)")
	rootCmd.PersistentFlags().String("log-format", "text", "log format (text, json)")

	// Set up flag change callbacks to immediately disable console output when log-file is specified
	rootCmd.PersistentFlags().String("test.log-file", "", "test log file path (used by test commands)")

	// Bind flags to viper
	viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))
	viper.BindPFlag("trace-provider", rootCmd.PersistentFlags().Lookup("trace-provider"))
	viper.BindPFlag("langfuse-host", rootCmd.PersistentFlags().Lookup("langfuse-host"))
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
	viper.BindPFlag("max-turns", rootCmd.PersistentFlags().Lookup("max-turns"))
	viper.BindPFlag("temperature", rootCmd.PersistentFlags().Lookup("temperature"))
	viper.BindPFlag("log-file", rootCmd.PersistentFlags().Lookup("log-file"))
	viper.BindPFlag("log-level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("log-format", rootCmd.PersistentFlags().Lookup("log-format"))
	viper.BindPFlag("test.log-file", rootCmd.PersistentFlags().Lookup("test.log-file"))

	// Add command groups
	rootCmd.AddCommand(mcp.MCPCmd)
	rootCmd.AddCommand(server.ServerCmd)
	rootCmd.AddCommand(testing.TestingCmd)
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	// Load .env file first (if present) - look in agent_go directory
	if err := godotenv.Load("agent_go/.env"); err == nil {
		// Environment loaded successfully
	} else if err := godotenv.Load(".env"); err == nil {
		// Environment loaded successfully
	} else if err := godotenv.Load("../.env"); err == nil {
		// Environment loaded successfully
	} else {
		fmt.Fprintln(os.Stderr, "No .env file found, using system environment variables")
	}

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".mcp-agent" (without extension).
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".mcp-agent")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}

	// Don't initialize logger globally - let individual commands handle it
	// This allows each command to set its own logging configuration
}
