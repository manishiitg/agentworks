package codingagentsetup

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrDoctorFailed = errors.New("one or more required dependencies are unavailable")

type DoctorOptions struct {
	Providers    []string
	Workspaces   []string
	Binary       string
	SkipMCPCheck bool
}

type DoctorReport struct {
	Healthy   bool                `json:"healthy"`
	TmuxReady bool                `json:"tmux_ready"`
	MCPReady  bool                `json:"mcp_ready"`
	Providers []ProviderReadiness `json:"providers"`
}

func (e *Environment) Doctor(ctx context.Context, options DoctorOptions) error {
	providers := splitList(options.Providers)
	var err error
	if len(providers) == 0 {
		for _, agent := range supportedAgents {
			providers = append(providers, agent.Provider)
		}
	}
	providers, err = validateProviders(providers)
	if err != nil {
		return err
	}
	workspaces, err := resolveWorkspaces(options.Workspaces)
	if err != nil {
		return err
	}

	report := DoctorReport{
		TmuxReady: e.cliInstalled("tmux"),
		Providers: e.ProviderReadiness(ctx, providers),
	}
	if options.SkipMCPCheck {
		report.MCPReady = true
	} else if binary, binaryErr := e.resolveBinary(options.Binary); binaryErr == nil {
		report.MCPReady = e.smoke(ctx, binary, providers, workspaces) == nil
	}
	report.Healthy = report.TmuxReady && report.MCPReady
	for _, provider := range report.Providers {
		report.Healthy = report.Healthy && provider.Ready
	}

	e.printDoctorReport(report)
	if !report.Healthy {
		return ErrDoctorFailed
	}
	return nil
}

func (e *Environment) printDoctorReport(report DoctorReport) {
	fmt.Fprintln(e.out, "llm-provider MCP doctor")
	if report.TmuxReady {
		fmt.Fprintln(e.out, "  ok       tmux")
	} else {
		fmt.Fprintln(e.out, "  missing  tmux (required to run delegated jobs)")
	}
	for _, provider := range report.Providers {
		switch provider.Status {
		case "ready":
			detail := "authenticated"
			if provider.ModelsAvailable > 0 {
				detail = fmt.Sprintf("authenticated, %d models available", provider.ModelsAvailable)
			}
			fmt.Fprintf(e.out, "  ok       %s (%s): %s\n", provider.DisplayName, provider.CLI, detail)
		case "missing":
			fmt.Fprintf(e.out, "  missing  %s (%s)\n", provider.DisplayName, provider.CLI)
		default:
			fmt.Fprintf(e.out, "  not ready %s: run %s\n", provider.DisplayName, provider.LoginCommand)
		}
	}
	if report.MCPReady {
		fmt.Fprintln(e.out, "  ok       MCP initialize and tools/list")
	} else {
		fmt.Fprintln(e.out, "  failed   MCP initialize and tools/list")
	}
	if report.Healthy {
		ready := make([]string, 0, len(report.Providers))
		for _, provider := range report.Providers {
			ready = append(ready, provider.Provider)
		}
		fmt.Fprintf(e.out, "Ready for: %s\n", strings.Join(ready, ", "))
	}
}
