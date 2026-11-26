package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentmodels"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentsetup"
	"github.com/spf13/cobra"
)

func newSetupCommand() *cobra.Command {
	var options codingagentsetup.SetupOptions
	var nonInteractive bool
	command := &cobra.Command{
		Use:   "setup",
		Short: "Interactively detect CLIs and register the MCP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Interactive = !nonInteractive && inputIsTerminal(cmd.InOrStdin())
			environment := codingagentsetup.NewEnvironment(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			return environment.Setup(cmd.Context(), options)
		},
	}
	command.Flags().StringVar(&options.Client, "client", "", "host registration: none, codex, claude, or both")
	command.Flags().StringSliceVar(&options.Providers, "providers", nil, "delegation target provider IDs")
	command.Flags().StringSliceVar(&options.Workspaces, "workspace", nil, "optional allowed workspace root (repeatable)")
	command.Flags().StringVar(&options.Binary, "binary", "", "MCP server binary path (default: this executable)")
	command.Flags().BoolVar(&options.SkipSmokeTest, "skip-smoke-test", false, "skip MCP initialize and tools/list verification")
	command.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt; defaults to binary-only registration")
	return command
}

func newDoctorCommand() *cobra.Command {
	var options codingagentsetup.DoctorOptions
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check MCP protocol and delegated CLI dependencies",
		RunE: func(cmd *cobra.Command, _ []string) error {
			environment := codingagentsetup.NewEnvironment(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			return environment.Doctor(cmd.Context(), options)
		},
	}
	command.Flags().StringSliceVar(&options.Providers, "providers", nil, "delegation target provider IDs")
	command.Flags().StringSliceVar(&options.Workspaces, "workspace", nil, "optional allowed workspace root (repeatable)")
	command.Flags().StringVar(&options.Binary, "binary", "", "MCP server binary path (default: this executable)")
	command.Flags().BoolVar(&options.SkipMCPCheck, "skip-mcp-check", false, "skip MCP initialize and tools/list verification")
	return command
}

func newModelsCommand() *cobra.Command {
	var live bool
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "models [provider]",
		Short: "List coding-agent model selectors",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if live {
				if len(args) != 1 {
					return fmt.Errorf("--live requires a provider")
				}
				return runLiveModelList(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
			}
			if len(args) == 1 {
				catalog, err := codingagentmodels.List(args[0])
				if err != nil {
					return err
				}
				return printCatalogs(cmd.OutOrStdout(), []codingagentmodels.Catalog{catalog}, jsonOutput)
			}
			return printCatalogs(cmd.OutOrStdout(), codingagentmodels.ListAll(), jsonOutput)
		},
	}
	command.Flags().BoolVar(&live, "live", false, "ask the provider CLI for its current model catalog")
	command.Flags().BoolVar(&jsonOutput, "json", false, "print the curated catalog as JSON")
	return command
}

func newUninstallCommand() *cobra.Command {
	var options codingagentsetup.UninstallOptions
	var nonInteractive bool
	command := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove MCP host registrations and optionally this binary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Interactive = !nonInteractive && inputIsTerminal(cmd.InOrStdin())
			environment := codingagentsetup.NewEnvironment(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			return environment.Uninstall(cmd.Context(), options)
		},
	}
	command.Flags().StringVar(&options.Client, "client", "", "host registration to remove: none, codex, claude, or both")
	command.Flags().StringVar(&options.Binary, "binary", "", "MCP server binary path (default: this executable)")
	command.Flags().BoolVar(&options.RemoveBinary, "remove-binary", false, "also delete the MCP executable")
	command.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt")
	return command
}

func printCatalogs(out io.Writer, catalogs []codingagentmodels.Catalog, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(catalogs)
	}
	for index, catalog := range catalogs {
		if index > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, catalog.Provider)
		for _, model := range catalog.Models {
			fmt.Fprintf(out, "  %-38s %s\n", model.ID, model.Name)
		}
		if catalog.LiveListCommand != "" {
			fmt.Fprintf(out, "  Live catalog: %s\n", catalog.LiveListCommand)
		}
		if catalog.Note != "" {
			fmt.Fprintf(out, "  %s\n", catalog.Note)
		}
	}
	return nil
}

func runLiveModelList(ctx context.Context, out, errOut io.Writer, provider string) error {
	name, args, ok := codingagentmodels.LiveCommand(provider)
	if !ok {
		return fmt.Errorf("provider %q does not expose a live model-list command; use the curated list", provider)
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = out
	command.Stderr = errOut
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return nil
}

func inputIsTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
