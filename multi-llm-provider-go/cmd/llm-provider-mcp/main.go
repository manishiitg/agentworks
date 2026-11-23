package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentjob"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingagentmcp"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var statePath string
	root := &cobra.Command{
		Use:           "llm-provider-mcp",
		Short:         "Delegate asynchronous jobs between coding-agent CLIs over MCP",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(statePath)
		},
	}
	root.Flags().StringVar(&statePath, "state", "", "path to the durable coding-agent job database")

	var workerJobID string
	var workerStatePath string
	worker := &cobra.Command{
		Use:    "worker",
		Short:  "Run one detached coding-agent job",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorker(workerStatePath, workerJobID)
		},
	}
	worker.Flags().StringVar(&workerJobID, "job-id", "", "job ID to execute")
	worker.Flags().StringVar(&workerStatePath, "state", "", "path to the durable coding-agent job database")
	_ = worker.MarkFlagRequired("job-id")
	_ = worker.MarkFlagRequired("state")
	root.AddCommand(worker, newSetupCommand(), newDoctorCommand(), newModelsCommand(), newUninstallCommand())
	return root
}

func runServer(configuredStatePath string) error {
	statePath, err := resolveStatePath(configuredStatePath)
	if err != nil {
		return err
	}
	store, err := codingagentjob.OpenStore(statePath)
	if err != nil {
		return err
	}
	defer store.Close()

	launcher := codingagentjob.ProcessLauncher{StatePath: store.Path()}
	options := []codingagentjob.ManagerOption{
		codingagentjob.WithDelegationDepth(codingagentjob.DelegationDepthFromEnvironment(), 1),
	}
	if providers := codingagentjob.SplitEnvironmentList(codingagentjob.EnvAllowedProviders); len(providers) > 0 {
		options = append(options, codingagentjob.WithAllowedProviders(providers))
	}
	if roots := codingagentjob.SplitEnvironmentList(codingagentjob.EnvWorkspaceRoots); len(roots) > 0 {
		options = append(options, codingagentjob.WithWorkspaceRoots(roots))
	}
	manager, err := codingagentjob.NewManager(store, launcher, options...)
	if err != nil {
		return err
	}
	mcpServer, err := codingagentmcp.New(manager, version)
	if err != nil {
		return err
	}
	return mcpServer.ServeStdio()
}

func runWorker(statePath, jobID string) error {
	store, err := codingagentjob.OpenStore(strings.TrimSpace(statePath))
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return codingagentjob.RunWorker(ctx, store, strings.TrimSpace(jobID), codingagentjob.ProviderRunner{Logger: os.Stderr})
}

func resolveStatePath(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}
	return codingagentjob.DefaultStatePath()
}
