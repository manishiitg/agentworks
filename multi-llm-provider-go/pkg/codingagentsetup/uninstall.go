package codingagentsetup

import (
	"context"
	"fmt"
)

type UninstallOptions struct {
	Client       string
	Binary       string
	Interactive  bool
	RemoveBinary bool
}

func (e *Environment) Uninstall(ctx context.Context, options UninstallOptions) error {
	hosts, err := e.resolveHosts(options.Client, options.Interactive)
	if err != nil {
		return err
	}
	project := ""
	if len(hosts) > 0 {
		project, err = e.workingDir()
		if err != nil {
			return err
		}
	}
	for _, host := range hosts {
		var output []byte
		removedRegistration := false
		switch host {
		case "codex":
			if _, getErr := e.runner.Run(ctx, "codex", "mcp", "get", ServerName); getErr != nil {
				fmt.Fprintf(e.out, "No %s registration found in %s.\n", ServerName, host)
				break
			}
			output, err = e.runner.Run(ctx, "codex", "mcp", "remove", ServerName)
			removedRegistration = err == nil
		case "claude":
			if _, getErr := e.runner.Run(ctx, "claude", "mcp", "get", ServerName); getErr != nil {
				fmt.Fprintf(e.out, "No %s registration found in %s.\n", ServerName, host)
				break
			}
			output, err = e.runner.Run(ctx, "claude", "mcp", "remove", "--scope", "local", ServerName)
			removedRegistration = err == nil
		}
		if err != nil {
			return commandError("remove MCP registration from "+host, output, err)
		}
		if removedRegistration {
			fmt.Fprintf(e.out, "Removed %s registration from %s.\n", ServerName, host)
		}
	}

	removedSkills, keptSkills, err := removeManagedDelegationSkills(project, hosts)
	if err != nil {
		return err
	}
	for _, path := range removedSkills {
		fmt.Fprintf(e.out, "Removed managed delegation skill %s.\n", path)
	}
	for _, path := range keptSkills {
		fmt.Fprintf(e.out, "Kept unmanaged delegation skill %s.\n", path)
	}

	removeBinary := options.RemoveBinary
	if options.Interactive && !removeBinary {
		confirmed, promptErr := e.confirm("Also remove the llm-provider-mcp binary?", false)
		if promptErr != nil {
			return promptErr
		}
		removeBinary = confirmed
	}
	if removeBinary {
		binary, resolveErr := e.resolveBinary(options.Binary)
		if resolveErr != nil {
			return resolveErr
		}
		if err := e.remove(binary); err != nil {
			return fmt.Errorf("remove MCP executable %q: %w", binary, err)
		}
		fmt.Fprintf(e.out, "Removed binary %s.\n", binary)
	}
	return nil
}
