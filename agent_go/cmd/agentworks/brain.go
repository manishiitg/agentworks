package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var brainSecretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// brainCommand is the person's own side of Brain administration. Agents manage everything else through the brain_admin
// tool; a secret's VALUE is typed here with hidden input and goes straight to the server over the person's own sign-in,
// never through an agent conversation (owner decision, 2026-10-12).
func brainCommand(o *options) *cobra.Command {
	brain := &cobra.Command{Use: "brain", Short: "Brain administration for the signed-in person"}
	secret := &cobra.Command{Use: "secret", Short: "Brain's own secrets (for example the Git backup token)"}
	secret.AddCommand(&cobra.Command{
		Use: "set NAME", Args: cobra.ExactArgs(1), Short: "Set a Brain secret; the value is typed with hidden input",
		Long: "Prompts for the value with hidden input in a real terminal, sends it over your own sign-in and prints only the name. " +
			"It refuses when stdin is not a terminal or when run under a coding agent: type it yourself.\n" +
			"Name the secret in the backup setup (brain_access configure_backup pat_secret=NAME) so no token passes through a chat.",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if !brainSecretNamePattern.MatchString(name) {
				return errors.New("secret names use letters, digits and underscores, starting with a letter")
			}
			if err := personTerminal("brain secret set", "set a Brain secret", "run `agentworks brain secret set "+name+"` yourself in your own terminal"); err != nil {
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
			result, err := client.BrainSecretSet(cmd.Context(), name, string(value))
			for i := range value {
				value[i] = 0
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(o.stderr, "Set %s.\n", name)
			return o.output(map[string]any{"secret": name, "result": result})
		},
	})
	secret.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List the names of Brain's secrets (never values)", RunE: func(cmd *cobra.Command, _ []string) error {
		return o.callWithArguments(cmd, "brain_admin", map[string]any{"action": "secrets_list"})
	}})
	secret.AddCommand(&cobra.Command{Use: "delete NAME", Args: cobra.ExactArgs(1), Short: "Delete a Brain secret by name", RunE: func(cmd *cobra.Command, args []string) error {
		return o.callWithArguments(cmd, "brain_admin", map[string]any{"action": "secrets_delete", "name": strings.TrimSpace(args[0])})
	}})
	brain.AddCommand(secret)
	return brain
}
