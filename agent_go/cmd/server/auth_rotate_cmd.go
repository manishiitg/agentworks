package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/sealbox"
	"github.com/spf13/cobra"
)

var rotateAuthSecretCmd = &cobra.Command{
	Use:   "rotate-auth-secret",
	Short: "Rotate AUTH_SECRET: re-encrypt every stored secret and update the env file",
	Long: `Re-encrypt everything sealed with the current AUTH_SECRET under a new one:
config/provider-api-keys.json plus every workflow secrets and workflow
provider-credentials document in the chat store. Optionally writes the new
secret into an env file.

Run it offline with the agent, workspace, and gateway services stopped;
existing sessions are invalidated by the rotation and users sign in again.
Exit code is non-zero when anything fails, so deploy scripts can gate the
restart on it. Point --env-file at the deployment's service env:
rootless $REMOTE_APP/.env, AWS /opt/video-studio/.env, dominion
/srv/dominion/.env. Pass --dry-run first to report what would change.`,
	RunE: runRotateAuthSecret,
}

func init() {
	rotateAuthSecretCmd.Flags().String("docs-dir", os.Getenv("WORKSPACE_DOCS_PATH"), "Workspace docs root (defaults to WORKSPACE_DOCS_PATH)")
	rotateAuthSecretCmd.Flags().String("file", "", "Path to config/provider-api-keys.json (defaults to <docs-dir>/config/provider-api-keys.json)")
	rotateAuthSecretCmd.Flags().String("old-auth-secret", "", "Current AUTH_SECRET (defaults to the AUTH_SECRET environment variable)")
	rotateAuthSecretCmd.Flags().String("new-auth-secret", "", "New AUTH_SECRET to rotate to")
	rotateAuthSecretCmd.Flags().Bool("generate-new-auth-secret", false, "Generate a new random AUTH_SECRET")
	rotateAuthSecretCmd.Flags().Bool("write-env", true, "Write the new AUTH_SECRET into the env file")
	rotateAuthSecretCmd.Flags().String("env-file", "agent_go/.env", "Env file to update with the new AUTH_SECRET")
	rotateAuthSecretCmd.Flags().Bool("backup", true, "Create timestamped backups before rewriting anything")
	rotateAuthSecretCmd.Flags().Bool("dry-run", false, "Report what would be rotated without writing anything")
}

type authSecretRotationOptions struct {
	DocsDir           string
	ProviderKeysFile  string
	OldSecret         string
	NewSecret         string
	GenerateNew       bool
	EnvFile           string
	WriteEnv          bool
	Backup            bool
	DryRun            bool
}

type authSecretRotationReport struct {
	ProviderKeysRotated bool
	SecretDocs          int
	SecretsRotated      int
	EnvUpdated          bool
}

func runRotateAuthSecret(cmd *cobra.Command, _ []string) error {
	docsDir, _ := cmd.Flags().GetString("docs-dir")
	filePath, _ := cmd.Flags().GetString("file")
	if !cmd.Flags().Changed("file") && docsDir != "" {
		filePath = filepath.Join(docsDir, "config", "provider-api-keys.json")
	}
	oldSecret, _ := cmd.Flags().GetString("old-auth-secret")
	newSecret, _ := cmd.Flags().GetString("new-auth-secret")
	generateNew, _ := cmd.Flags().GetBool("generate-new-auth-secret")
	writeEnv, _ := cmd.Flags().GetBool("write-env")
	envFile, _ := cmd.Flags().GetString("env-file")
	backup, _ := cmd.Flags().GetBool("backup")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if generateNew {
		if newSecret != "" {
			return fmt.Errorf("--new-auth-secret and --generate-new-auth-secret cannot be used together")
		}
		generated, err := generateAuthSecretHex(32)
		if err != nil {
			return fmt.Errorf("failed to generate auth secret: %w", err)
		}
		newSecret = generated
	}
	if err := ValidateAuthSecretValue(newSecret); err != nil {
		return fmt.Errorf("invalid new AUTH_SECRET: %w", err)
	}
	if strings.TrimSpace(oldSecret) == "" {
		oldSecret = string(GetAuthSecret())
	}
	if strings.TrimSpace(oldSecret) == "" {
		return fmt.Errorf("current AUTH_SECRET is required; set it in the environment or pass --old-auth-secret")
	}
	if strings.TrimSpace(docsDir) == "" {
		return fmt.Errorf("--docs-dir (or WORKSPACE_DOCS_PATH) is required")
	}

	report, err := rotateAuthSecret(authSecretRotationOptions{
		DocsDir:          docsDir,
		ProviderKeysFile: filePath,
		OldSecret:        oldSecret,
		NewSecret:        newSecret,
		GenerateNew:      generateNew,
		EnvFile:          envFile,
		WriteEnv:         writeEnv,
		Backup:           backup,
		DryRun:           dryRun,
	}, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if dryRun {
		fmt.Fprintln(out, "Dry run: nothing was written.")
	}
	fmt.Fprintf(out, "Provider keys file rotated: %v\n", report.ProviderKeysRotated)
	fmt.Fprintf(out, "Workflow secret documents rotated: %d (%d secrets)\n", report.SecretDocs, report.SecretsRotated)
	fmt.Fprintf(out, "Env file updated: %v\n", report.EnvUpdated)
	if !dryRun {
		fmt.Fprintln(out, "Restart the agent, workspace, and gateway services so they load the new AUTH_SECRET.")
		fmt.Fprintln(out, "All existing sessions are invalidated; users sign in again.")
	}
	return nil
}

// rotateAuthSecret re-encrypts the provider keys file and every workflow
// secrets/credentials document from oldSecret to newSecret. It is two-phase
// and all-or-nothing: every ciphertext is decrypted with the old secret
// first, and nothing is written unless all of them open. A blob that no
// longer decrypts (corrupt, or sealed under a still older secret) aborts the
// run with the offending document named; remove or repair it and re-run.
func rotateAuthSecret(opts authSecretRotationOptions, out io.Writer) (*authSecretRotationReport, error) {
	report := &authSecretRotationReport{}
	oldKey := deriveSecretsKeyFromSecret([]byte(opts.OldSecret))
	newKey := deriveSecretsKeyFromSecret([]byte(opts.NewSecret))

	// Phase 1a: provider keys file (absent on fresh installs: not an error).
	if opts.ProviderKeysFile != "" {
		if _, err := os.Stat(opts.ProviderKeysFile); err == nil {
			if err := rekeyProviderKeysFile(opts.ProviderKeysFile, []byte(opts.OldSecret), []byte(opts.NewSecret), opts.Backup && !opts.DryRun, out, opts.DryRun); err != nil {
				return nil, err
			}
			report.ProviderKeysRotated = !opts.DryRun
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat provider key file %s: %w", opts.ProviderKeysFile, err)
		}
	}

	// Phase 1b: decrypt every workflow secret with the old key before writing.
	docs, err := collectWorkflowSecretDocs(opts.DocsDir)
	if err != nil {
		return nil, err
	}
	type rekeyedDoc struct {
		doc       *workflowSecretDocFile
		plaintext map[string]string
	}
	rekeyed := make([]rekeyedDoc, 0, len(docs))
	for _, doc := range docs {
		plaintext := make(map[string]string, len(doc.records))
		for name, rec := range doc.records {
			aad, err := doc.aad()
			if err != nil {
				return nil, fmt.Errorf("cannot rotate %s: %w", doc.describe(), err)
			}
			opened, err := sealbox.Open(oldKey, rec.EncryptedValue, aad)
			if err != nil {
				return nil, fmt.Errorf("cannot decrypt %s secret %q with the old AUTH_SECRET (remove or repair it and re-run): %w", doc.describe(), name, err)
			}
			plaintext[name] = opened
		}
		rekeyed = append(rekeyed, rekeyedDoc{doc: doc, plaintext: plaintext})
		report.SecretsRotated += len(plaintext)
	}
	report.SecretDocs = len(rekeyed)
	if opts.DryRun {
		return report, nil
	}

	// Phase 2: backup, re-encrypt, write, verify.
	stamp := time.Now().UTC().Format("20060102-150405")
	for _, r := range rekeyed {
		if opts.Backup {
			raw, err := os.ReadFile(r.doc.path)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s for backup: %w", r.doc.describe(), err)
			}
			backupPath := fmt.Sprintf("%s.bak-%s", r.doc.path, stamp)
			if err := os.WriteFile(backupPath, raw, 0600); err != nil {
				return nil, fmt.Errorf("failed to write backup file %s: %w", backupPath, err)
			}
			fmt.Fprintf(out, "Backed up %s to %s\n", r.doc.describe(), backupPath)
		}
		now := time.Now().UTC()
		for name, rec := range r.doc.records {
			aad, err := r.doc.aad()
			if err != nil {
				return nil, fmt.Errorf("cannot rotate %s: %w", r.doc.describe(), err)
			}
			resealed, err := sealbox.Seal(newKey, r.plaintext[name], aad)
			if err != nil {
				return nil, fmt.Errorf("failed to re-encrypt %s secret %q: %w", r.doc.describe(), name, err)
			}
			rec.EncryptedValue = resealed
			rec.UpdatedAt = now
		}
		if err := r.doc.write(); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", r.doc.describe(), err)
		}
	}
	for _, r := range rekeyed {
		if err := r.doc.verify(newKey); err != nil {
			return nil, fmt.Errorf("failed to verify %s with the new AUTH_SECRET: %w", r.doc.describe(), err)
		}
	}

	if opts.WriteEnv {
		if err := upsertEnvVar(opts.EnvFile, "AUTH_SECRET", opts.NewSecret); err != nil {
			return nil, fmt.Errorf("failed to update %s: %w", opts.EnvFile, err)
		}
		fmt.Fprintf(out, "Updated %s with a new AUTH_SECRET\n", opts.EnvFile)
		report.EnvUpdated = true
	}
	return report, nil
}

// rekeyProviderKeysFile decrypts the provider key file with oldSecret and
// re-encrypts it with newSecret, with an optional timestamped backup and a
// decrypt-with-new verification read. Shared by both rotation commands. In
// dry-run mode it only proves the file opens with the old secret.
func rekeyProviderKeysFile(filePath string, oldSecret, newSecret []byte, backup bool, out io.Writer, dryRun bool) error {
	encryptedBase64, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read provider key file %s: %w", filePath, err)
	}
	encryptedPayload := strings.TrimSpace(string(encryptedBase64))
	if encryptedPayload == "" {
		return fmt.Errorf("provider key file %s is empty", filePath)
	}
	encryptedBytes, err := base64.StdEncoding.DecodeString(encryptedPayload)
	if err != nil {
		return fmt.Errorf("failed to decode provider key file from base64: %w", err)
	}
	plaintext, err := decryptProviderKeysWithSecret(encryptedBytes, oldSecret)
	if err != nil {
		return fmt.Errorf("failed to decrypt provider keys with the old AUTH_SECRET: %w", err)
	}
	var keys StoredProviderKeys
	if err := json.Unmarshal(plaintext, &keys); err != nil {
		return fmt.Errorf("failed to parse decrypted provider keys: %w", err)
	}
	if dryRun {
		fmt.Fprintf(out, "Provider keys file %s opens with the old AUTH_SECRET\n", filePath)
		return nil
	}

	if backup {
		backupPath := fmt.Sprintf("%s.bak-%s", filePath, time.Now().UTC().Format("20060102-150405"))
		if err := os.WriteFile(backupPath, encryptedBase64, 0600); err != nil {
			return fmt.Errorf("failed to write backup file %s: %w", backupPath, err)
		}
		fmt.Fprintf(out, "Backed up existing encrypted provider keys to %s\n", backupPath)
	}

	reencrypted, err := encryptProviderKeysWithSecret(plaintext, newSecret)
	if err != nil {
		return fmt.Errorf("failed to re-encrypt provider keys: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create provider key directory: %w", err)
	}
	if err := os.WriteFile(filePath, []byte(base64.StdEncoding.EncodeToString(reencrypted)), 0600); err != nil {
		return fmt.Errorf("failed to write re-encrypted provider key file: %w", err)
	}

	verificationPayload, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to re-read rotated provider key file: %w", err)
	}
	verificationBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(verificationPayload)))
	if err != nil {
		return fmt.Errorf("failed to decode rotated provider key file for verification: %w", err)
	}
	if _, err := decryptProviderKeysWithSecret(verificationBytes, newSecret); err != nil {
		return fmt.Errorf("failed to verify rotated provider key file with new AUTH_SECRET: %w", err)
	}
	fmt.Fprintf(out, "Re-encrypted provider keys at %s with the new AUTH_SECRET\n", filePath)
	return nil
}

// rotateSecretRecord mirrors chathistory's stored record shape.
type rotateSecretRecord struct {
	EncryptedValue string    `json:"encrypted_value"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// workflowSecretDocFile is one _users/<uid>/workflow_secrets/*.json or
// workflow_provider_credentials/*.json document parsed for rotation.
type workflowSecretDocFile struct {
	path         string
	userID       string
	kind         string // "secrets" or "credentials"
	workflowPath string
	records      map[string]*rotateSecretRecord
}

func (d *workflowSecretDocFile) describe() string {
	return fmt.Sprintf("%s document %s", d.kind, d.path)
}

// aad rebuilds the seal AAD exactly as the writers do: shared workflow
// secrets bind to the workflow path, per-user and credential documents bind
// to the storing user ID.
func (d *workflowSecretDocFile) aad() ([]byte, error) {
	if d.kind == "secrets" && d.userID == chathistory.SharedWorkflowSecretsUserID {
		return sharedWorkflowSecretAAD(d.workflowPath)
	}
	return []byte(d.userID), nil
}

func (d *workflowSecretDocFile) write() error {
	if d.kind == "credentials" {
		return fsutil.WriteJSONAtomic(d.path, map[string]any{
			"workflow_path": d.workflowPath,
			"credentials":   d.records,
		}, 0600)
	}
	return fsutil.WriteJSONAtomic(d.path, map[string]any{
		"workflow_path": d.workflowPath,
		"secrets":       d.records,
	}, 0600)
}

func (d *workflowSecretDocFile) verify(key []byte) error {
	raw, err := os.ReadFile(d.path)
	if err != nil {
		return err
	}
	var parsed struct {
		Secrets     map[string]*rotateSecretRecord `json:"secrets"`
		Credentials map[string]*rotateSecretRecord `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	records := parsed.Secrets
	if d.kind == "credentials" {
		records = parsed.Credentials
	}
	for name, rec := range records {
		aad, err := d.aad()
		if err != nil {
			return err
		}
		if _, err := sealbox.Open(key, rec.EncryptedValue, aad); err != nil {
			return fmt.Errorf("secret %q: %w", name, err)
		}
	}
	return nil
}

// collectWorkflowSecretDocs finds every workflow secrets and provider
// credentials document under the docs root, in stable order.
func collectWorkflowSecretDocs(docsDir string) ([]*workflowSecretDocFile, error) {
	var docs []*workflowSecretDocFile
	for _, kind := range []struct {
		dir     string
		name    string
		jsonKey string
	}{
		{"workflow_secrets", "secrets", "secrets"},
		{"workflow_provider_credentials", "credentials", "credentials"},
	} {
		matches, err := filepath.Glob(filepath.Join(docsDir, "_users", "*", kind.dir, "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		for _, path := range matches {
			rel, err := filepath.Rel(filepath.Join(docsDir, "_users"), path)
			if err != nil {
				return nil, err
			}
			userID := strings.Split(rel, string(filepath.Separator))[0]
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s: %w", path, err)
			}
			var parsed struct {
				WorkflowPath string                          `json:"workflow_path"`
				Secrets      map[string]*rotateSecretRecord `json:"secrets"`
				Credentials  map[string]*rotateSecretRecord `json:"credentials"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", path, err)
			}
			records := parsed.Secrets
			if kind.name == "credentials" {
				records = parsed.Credentials
			}
			if len(records) == 0 {
				continue
			}
			docs = append(docs, &workflowSecretDocFile{
				path:         path,
				userID:       userID,
				kind:         kind.name,
				workflowPath: parsed.WorkflowPath,
				records:      records,
			})
		}
	}
	return docs, nil
}
