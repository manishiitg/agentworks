package server

import (
	"encoding/json"
	"fmt"
	"io"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/spf13/cobra"
)

// The one-off converter submits its complete candidate before writing any files.
// Use the runtime's validators so migration cannot invent a second input contract.
func validateWorkflowInputMigration(reader io.Reader) error {
	var candidate struct {
		Definitions VariablesManifest           `json:"definitions"`
		Functions   []projectFunctionDefinition `json:"functions"`
	}
	if err := json.NewDecoder(reader).Decode(&candidate); err != nil {
		return err
	}
	if err := workflow.ValidateWorkflowInputDefinitions(&candidate.Definitions); err != nil {
		return fmt.Errorf("workflow Inputs: %w", err)
	}
	for _, fn := range candidate.Functions {
		if fn.Action.Type != "run" {
			continue
		}
		var input scheduleExecutionInput
		if len(fn.Input) != 0 {
			if err := json.Unmarshal(fn.Input, &input); err != nil {
				return fmt.Errorf("function %q: invalid saved inputs", fn.Name)
			}
		}
		if input.Groups != nil {
			return fmt.Errorf("function %q: convert retired groups before input contracts", fn.Name)
		}
		exposed := fn.Inputs
		if fn.InputMapping != nil {
			exposed = nil
			if fn.InputMapping.Mode == "envelope" {
				exposed = fn.InputMapping.Variables
			}
		} else if fn.InputSchema != nil {
			return fmt.Errorf("function %q: copied schema remains", fn.Name)
		}
		if _, err := workflowCallerInputSchema(&candidate.Definitions, exposed, input.InputPreset, input.Inputs); err != nil {
			return fmt.Errorf("function %q: %w", fn.Name, err)
		}
	}
	return nil
}

func init() {
	ServerCmd.AddCommand(&cobra.Command{
		Use: "validate-workflow-input-migration", Short: "Check proposed workflow input migration JSON from stdin without writing files", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateWorkflowInputMigration(cmd.InOrStdin()); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Workflow input migration valid")
			return err
		},
	})
}
