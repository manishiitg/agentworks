package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "llm-chat",
		Short: "Interactive chat CLI with LLM models",
		Long:  "Interactive chat tool for LLM models with fixed tools, streaming, and parallel tool calls",
		Run: func(cmd *cobra.Command, args []string) {
			if err := RunChat(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
