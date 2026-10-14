package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

const maxDotenvBytes = 1 << 20

// secretCommand adds secrets the person gives, from the terminal or a .env file, to Vault or to a workflow, Relay or
// Crew. The value goes from the terminal or file straight to the server over the person's own sign-in; the output
// and errors name secrets and outcomes only, never a value.
func secretCommand(o *options) *cobra.Command {
	secret := &cobra.Command{Use: "secret", Short: "Add secrets to Vault or a project (values are never printed)"}
	var vault bool
	var workflow, crew, relay string
	flags := func(c *cobra.Command) {
		c.Flags().BoolVar(&vault, "vault", false, "add to Vault (a Vault administrator)")
		c.Flags().StringVar(&workflow, "workflow", "", "add to this workflow (its owner)")
		c.Flags().StringVar(&crew, "crew", "", "add to this Crew (its owner)")
		c.Flags().StringVar(&relay, "relay", "", "add to this Relay (its owner)")
	}
	target := func() (agentworksclient.SecretTarget, error) {
		var picked []agentworksclient.SecretTarget
		if vault {
			picked = append(picked, agentworksclient.SecretTarget{Kind: "vault"})
		}
		for kind, id := range map[string]string{"workflow": workflow, "crew": crew, "relay": relay} {
			if strings.TrimSpace(id) != "" {
				picked = append(picked, agentworksclient.SecretTarget{Kind: kind, ID: strings.TrimSpace(id)})
			}
		}
		if len(picked) != 1 {
			return agentworksclient.SecretTarget{}, errors.New("choose exactly one of --vault, --workflow ID, --crew ID or --relay ID")
		}
		return picked[0], nil
	}

	set := &cobra.Command{
		Use: "set NAME", Args: cobra.ExactArgs(1), Short: "Add or replace one secret; the value is typed with hidden input",
		Long: "Prompts for the value with hidden input in a real terminal, sends it over your own sign-in and prints only the name. " +
			"It refuses when stdin is not a terminal or when run under a coding agent: type it yourself.",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if !secretNameForCLI(name) {
				return errors.New("secret names use letters, digits and underscores, starting with a letter or underscore")
			}
			t, err := target()
			if err != nil {
				return err
			}
			if err := personTerminal("secret set", "set a secret", "run `agentworks secret set "+name+" "+targetFlag(t)+"` yourself in your own terminal, or give the value to your agent"); err != nil {
				return err
			}
			fmt.Fprintf(o.stderr, "Value for %s (hidden): ", name)
			value, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(o.stderr)
			if err != nil {
				return fmt.Errorf("could not read the value: %w", err)
			}
			if strings.TrimSpace(string(value)) == "" {
				return errors.New("the value is empty; nothing was changed")
			}
			client, err := o.client()
			if err != nil {
				return err
			}
			raw, err := client.SecretsImport(cmd.Context(), t, map[string]string{name: string(value)}, nil, true, false)
			for i := range value {
				value[i] = 0
			}
			if err != nil {
				return err
			}
			return printSecretResults(o, raw)
		},
	}
	flags(set)

	var only string
	var overwrite, dryRun bool
	importCmd := &cobra.Command{
		Use: "import FILE", Args: cobra.ExactArgs(1), Short: "Add every secret in a .env file; existing names are skipped unless --overwrite",
		Long: "Reads KEY=VALUE lines (comments, `export` prefixes and quotes are understood) and sends the values straight to the server " +
			"over your own sign-in. Prints only names with added, updated or skipped. A coding agent may run this: the values never enter its transcript.\n" +
			"--only A,B limits the import to those names; --dry-run lists what would be added without sending any value.",
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := target()
			if err != nil {
				return err
			}
			data, err := readDotenvFile(args[0])
			if err != nil {
				return err
			}
			parsed := parseDotEnv(string(data))
			values := parsed.Values
			if strings.TrimSpace(only) != "" {
				wanted := map[string]bool{}
				for _, name := range strings.Split(only, ",") {
					wanted[strings.TrimSpace(name)] = true
				}
				for name := range values {
					if !wanted[name] {
						delete(values, name)
					}
				}
				for name := range wanted {
					if _, ok := values[name]; !ok && name != "" {
						parsed.Invalid = append(parsed.Invalid, "--only "+name+": not in the file")
					}
				}
			}
			for _, problem := range parsed.Invalid {
				fmt.Fprintln(o.stderr, "skipped "+problem)
			}
			if len(values) == 0 {
				return errors.New("no valid secrets to import")
			}
			client, err := o.client()
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if dryRun {
				names := make([]string, 0, len(values))
				for _, name := range parsed.Order {
					if _, ok := values[name]; ok {
						names = append(names, name)
					}
				}
				raw, err = client.SecretsImport(cmd.Context(), t, nil, names, overwrite, true)
			} else {
				raw, err = client.SecretsImport(cmd.Context(), t, values, nil, overwrite, false)
			}
			for name := range values {
				values[name] = ""
			}
			if err != nil {
				return err
			}
			return printSecretResults(o, raw)
		},
	}
	flags(importCmd)
	importCmd.Flags().StringVar(&only, "only", "", "comma-separated names to import")
	importCmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing names instead of skipping them")
	importCmd.Flags().BoolVar(&dryRun, "dry-run", false, "list the names that would be added or updated; send no value")
	secret.AddCommand(set, importCmd)
	return secret
}

func targetFlag(t agentworksclient.SecretTarget) string {
	if t.Kind == "vault" {
		return "--vault"
	}
	return "--" + t.Kind + " " + t.ID
}

// readDotenvFile reads a regular file of bounded size; the error never contains the file's content.
func readDotenvFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, errors.Unwrap(err))
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDotenvBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s", path)
	}
	if len(data) > maxDotenvBytes {
		return nil, fmt.Errorf("%s is larger than 1 MiB", path)
	}
	return data, nil
}

// printSecretResults prints one line per name (never a value) and the JSON answer on stdout.
func printSecretResults(o *options, raw json.RawMessage) error {
	var answer struct {
		Results []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return fmt.Errorf("unexpected answer from the server")
	}
	for _, result := range answer.Results {
		line := result.Status + " " + result.Name
		if result.Reason != "" {
			line += " (" + result.Reason + ")"
		}
		fmt.Fprintln(o.stderr, line)
	}
	return o.output(json.RawMessage(raw))
}
