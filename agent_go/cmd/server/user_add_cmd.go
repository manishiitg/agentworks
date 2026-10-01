package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// `server add-user` creates an account the way the Users panel does, for an administrator working on
// the host (deploy/rootless-linux/provision-slots.sh adduser calls it, then assigns a slot).
// Signing in never creates an account, so this is how a person gets one.
var addUserCmd = &cobra.Command{
	Use:   "add-user",
	Short: "Add a person to the user directory (an account an administrator provisions)",
	Long: `Adds a person by email with a role and products, using the same rules as the Users panel.
Prints one JSON line with the account's id. An email already in the directory is not an error: the
existing account is printed again, so the command is safe to repeat.

Run it with the service environment loaded (the deployment's .env), with the workspace service up.`,
	RunE: runAddUser,
}

func init() {
	addUserCmd.Flags().String("email", "", "The person's email address (their sign-in)")
	addUserCmd.Flags().String("username", "", "Username (defaults to the email address)")
	addUserCmd.Flags().String("role", "editor", "admin, creator, editor or viewer")
	addUserCmd.Flags().String("products", "code", "Comma-separated products the account may use")
}

// addDirectoryUser appends a new account to dir, or returns the existing one for that email.
func addDirectoryUser(dir *userDirectory, email, username, role string, products []string) (rec UserRecord, created bool, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return UserRecord{}, false, fmt.Errorf("a valid email address is required")
	}
	if existing := dir.byEmail(email); existing != nil {
		return *existing, false, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = email
	}
	if !validUsername(username) {
		return UserRecord{}, false, errUsernameInvalid
	}
	if dir.byUsername(username) != nil {
		return UserRecord{}, false, fmt.Errorf("a user with the username %q already exists", username)
	}
	if msg := adminEmailError(dir, email, ""); msg != "" {
		return UserRecord{}, false, fmt.Errorf("%s", msg)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec = UserRecord{ID: userIDForUsername(username), Username: username, Email: email, Products: []string{}, CreatedAt: now, UpdatedAt: now}
	if err := applyRoleWrite(&rec, userWriteRequest{Role: &role}); err != nil {
		return UserRecord{}, false, err
	}
	rec.Products = normalizeProducts(products)
	dir.Users = append(dir.Users, rec)
	return rec, true, nil
}

func runAddUser(cmd *cobra.Command, _ []string) error {
	email, _ := cmd.Flags().GetString("email")
	username, _ := cmd.Flags().GetString("username")
	role, _ := cmd.Flags().GetString("role")
	productList, _ := cmd.Flags().GetString("products")
	var products []string
	for _, p := range strings.Split(productList, ",") {
		if p = strings.TrimSpace(p); p != "" {
			products = append(products, p)
		}
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		return fmt.Errorf("read the user directory: %w", err)
	}
	rec, created, err := addDirectoryUser(dir, email, username, role, products)
	if err != nil {
		return err
	}
	if created {
		if err := saveUserDirectory(dir); err != nil {
			return fmt.Errorf("save the user directory: %w", err)
		}
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
		"id": rec.ID, "username": rec.Username, "email": rec.Email, "role": roleForRecord(&rec), "products": rec.Products, "created": created,
	})
}
