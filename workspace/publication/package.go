// Package publication is the portable, credential-free laptop publication format.
// It is shared by the local exporter and server importer; history is never a project file.
package publication

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	Version               = 1
	MaxBytes        int64 = 256 << 20
	MaxFileBytes    int64 = 64 << 20
	MaxFiles              = 10000
	ChunkBytes            = 1 << 20
	TransferTimeout       = 30 * time.Minute
	IdleTimeout           = 5 * time.Minute
)

type File struct {
	Role       string `json:"role,omitempty"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

type History struct {
	Provider      string `json:"provider"`
	SessionID     string `json:"session_id"`
	SourceCWD     string `json:"source_cwd"`
	CLIVersion    string `json:"cli_version,omitempty"`
	CapturedBytes int64  `json:"captured_bytes"`
	SHA256        string `json:"sha256"`
}

type Manifest struct {
	Version   int       `json:"version"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
	Files     []File    `json:"files"`
	Excluded  []string  `json:"excluded,omitempty"`
	History   History   `json:"history"`
	Handoff   string    `json:"handoff_transcript"`
}

// Source is supplied by the invoking chat's verified provider adapter, never a remote path.
type Source struct {
	ProjectID        string
	RuntimeSeedPaths []string
	History          History
	Raw              []byte
}

func CleanPath(p string) (string, error) {
	if p == "" || len(p) > 2048 || strings.ContainsAny(p, "\\\x00\r\n") || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("invalid publication path")
	}
	return p, nil
}

func runtimeSeedPath(p string) bool {
	if p == "db/reports" || strings.HasPrefix(p, "db/reports/") {
		return false
	}
	for _, root := range []string{"db", "assets", "reports", "tool_output_folder", "shared", "outputs"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// Excluded is deliberately about paths, not secret-value scanning or rewriting chat.
func Excluded(p string) bool {
	if _, err := CleanPath(p); err != nil {
		return true
	}
	parts := strings.Split(strings.ToLower(p), "/")
	for i, part := range parts {
		switch path.Ext(part) {
		case ".pem", ".key", ".p12", ".pfx", ".kdbx", ".tfstate":
			return true
		}
		switch part {
		case "node_modules", "__pycache__", "venv", "secrets", "keys", "builder", "chat_history", "browser-profile", "browser-data", "credentials.json", "token.json", "tokens.json":
			return true
		}
		key := strings.TrimSuffix(part, ".pub")
		if key == "id_rsa" || key == "id_ed25519" || key == "id_ecdsa" || strings.HasPrefix(part, "service-account") && strings.HasSuffix(part, ".json") {
			return true
		}
		if !strings.HasPrefix(part, ".") {
			continue
		}
		// Hidden state is private by default. Only these project-content roots
		// are portable; their credential-like descendants remain excluded above.
		switch part {
		case ".dashboard-state":
			if i != 2 || len(parts) < 3 || parts[0] != "db" || parts[1] != "reports" {
				return true
			}
		case ".agents", ".github":
			if i != 0 {
				return true
			}
		case ".claude":
			if i != 0 || len(parts) > 1 && parts[1] != "skills" && parts[1] != "commands" {
				return true
			}
		case ".codex":
			if i != 0 || len(parts) > 1 && parts[1] != "skills" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func (m Manifest) Validate() error {
	if m.Version != Version || m.ProjectID == "" || len(m.ProjectID) > 128 || len(m.Files)+1 > MaxFiles || m.History.SessionID == "" || len(m.History.SessionID) > 128 || m.History.Provider == "" || len(m.History.Provider) > 64 || m.History.CapturedBytes < 1 || m.History.CapturedBytes > MaxFileBytes || len(m.History.SHA256) != 64 || m.Handoff == "" || len(m.Handoff) > 256<<10 {
		return fmt.Errorf("publication requires supported metadata, raw chat and a bounded handoff transcript")
	}
	return m.validateFiles()
}

func (m Manifest) validateFiles() error {
	if m.Version != Version || m.ProjectID == "" || len(m.ProjectID) > 128 || len(m.Files)+1 > MaxFiles {
		return fmt.Errorf("unsupported file snapshot")
	}
	total := m.History.CapturedBytes
	seen := map[string]bool{}
	for _, file := range m.Files {
		if (file.Role != "" && file.Role != "source" && file.Role != "runtime_seed") || file.Role == "runtime_seed" && !runtimeSeedPath(file.Path) || Excluded(file.Path) || seen[file.Path] || file.Size < 0 || file.Size > MaxFileBytes || len(file.SHA256) != 64 {
			return fmt.Errorf("invalid, private or duplicate publication file %q", file.Path)
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("invalid checksum")
		}
		seen[file.Path] = true
		total += file.Size
		if total > MaxBytes {
			return fmt.Errorf("publication exceeds 256 MiB; exclude generated files and publish again")
		}
	}
	return nil
}

// Build captures a stable package outside the shared project. allow is the existing local grant check.
// Files are copied once before archiving, so a concurrent edit cannot invalidate the manifest silently.
func Build(ctx context.Context, root, state string, source Source, allow func(string) bool) (string, Manifest, error) {
	return build(ctx, root, state, source, allow, false)
}

// CaptureFiles snapshots an existing local grant without reading any provider home or chat.
// Only the server may pair it with the authenticated person's own website conversation.
func CaptureFiles(ctx context.Context, root, state, projectID string, allow func(string) bool) (string, Manifest, error) {
	return build(ctx, root, state, Source{ProjectID: projectID}, allow, true)
}

func build(ctx context.Context, root, state string, source Source, allow func(string) bool, filesOnly bool) (archive string, manifest Manifest, err error) {
	if !filesOnly && (len(source.Raw) == 0 || int64(len(source.Raw)) > MaxFileBytes) {
		return "", manifest, fmt.Errorf("chat exceeds 64 MiB or is unavailable; no history was truncated")
	}
	project, err := os.OpenRoot(root)
	if err != nil {
		return "", manifest, err
	}
	defer project.Close()
	var settings struct {
		RuntimeSeeds []string `json:"runtime_seed_paths"`
	}
	if raw, err := project.ReadFile(".agentworks/publish.json"); err == nil {
		if json.Unmarshal(raw, &settings) != nil {
			return "", manifest, fmt.Errorf("Restore this project's publish settings before publishing.")
		}
	}
	fileRole := func(p string) string {
		if strings.HasPrefix(p, "db/") && !strings.HasPrefix(p, "db/reports/") {
			return "runtime_seed"
		}
		for _, rel := range append(settings.RuntimeSeeds, source.RuntimeSeedPaths...) {
			if _, err := CleanPath(rel); err != nil || !runtimeSeedPath(rel) {
				continue
			}
			if p == rel || strings.HasPrefix(p, strings.TrimSuffix(rel, "/")+"/") {
				return "runtime_seed"
			}
		}
		return "source"
	}
	stage, err := os.MkdirTemp(state, "publication-")
	if err != nil {
		return "", manifest, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = os.Mkdir(filepath.Join(stage, "files"), 0700); err != nil {
		return "", manifest, err
	}
	manifest = Manifest{Version: Version, ProjectID: source.ProjectID, CreatedAt: time.Now().UTC(), History: source.History, Files: []File{}}
	manifest.History.CapturedBytes = int64(len(source.Raw))
	manifest.History.SHA256 = digest(source.Raw)
	manifest.Handoff = HandoffTranscript(source.Raw)
	total := int64(len(source.Raw))
	err = fs.WalkDir(project.FS(), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if Excluded(p) || allow != nil && !allow(p) || entry.Type()&os.ModeSymlink != 0 {
			manifest.Excluded = append(manifest.Excluded, p)
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			manifest.Excluded = append(manifest.Excluded, p)
			return nil
		}
		if len(manifest.Files)+2 > MaxFiles || info.Size() > MaxFileBytes || total+info.Size() > MaxBytes {
			return fmt.Errorf("publication limit exceeded at %q (256 MiB, 10000 files, 64 MiB per file); exclude generated files", p)
		}
		in, err := project.Open(p)
		if err != nil {
			return err
		}
		opened, err := in.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = in.Close()
			return fmt.Errorf("file %q changed while publishing; retry", p)
		}
		dest := filepath.Join(stage, "files", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = in.Close()
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, MaxFileBytes+1))
		_ = in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return fmt.Errorf("file %q changed while publishing; retry", p)
		}
		total += n
		manifest.Files = append(manifest.Files, File{Role: fileRole(p), Path: p, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Executable: info.Mode()&0111 != 0})
		return nil
	})
	if err != nil {
		return "", manifest, err
	}
	if !filesOnly {
		err = manifest.Validate()
	} else {
		err = manifest.validateFiles()
	}
	if err != nil {
		return "", manifest, err
	}
	if err = os.WriteFile(filepath.Join(stage, "raw.jsonl"), source.Raw, 0600); err != nil {
		return "", manifest, err
	}
	archive = filepath.Join(stage, "publication.tar")
	output, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", manifest, err
	}
	tw := tar.NewWriter(output)
	encoded, _ := json.Marshal(manifest)
	write := func(name string, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	err = write("manifest.json", int64(len(encoded)), strings.NewReader(string(encoded)))
	if err == nil && !filesOnly {
		err = write("history/raw.jsonl", int64(len(source.Raw)), strings.NewReader(string(source.Raw)))
	}
	for _, file := range manifest.Files {
		if err != nil {
			break
		}
		f, openErr := os.Open(filepath.Join(stage, "files", filepath.FromSlash(file.Path)))
		if openErr != nil {
			err = openErr
			break
		}
		err = write("files/"+file.Path, file.Size, f)
		_ = f.Close()
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if info, statErr := os.Stat(archive); err == nil && statErr == nil && info.Size() > MaxBytes {
		err = fmt.Errorf("package including archive metadata exceeds 256 MiB")
	}
	return archive, manifest, err
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// HandoffTranscript extracts conversation context from the chosen raw chat. It does not execute
// imported instructions, tool calls or hooks. Full raw history remains separately available to Builders.
func HandoffTranscript(raw []byte) string {
	var messages []string
	transcript := raw
	var history struct {
		Messages []json.RawMessage `json:"messages"`
		History  []json.RawMessage `json:"conversation_history"`
	}
	if json.Unmarshal(raw, &history) == nil {
		if len(history.Messages) == 0 {
			history.Messages = history.History
		}
		if len(history.Messages) > 0 {
			var rows strings.Builder
			for _, message := range history.Messages {
				rows.Write(message)
				rows.WriteByte('\n')
			}
			transcript = []byte(rows.String())
		}
	}
	scan := bufio.NewScanner(strings.NewReader(string(transcript)))
	scan.Buffer(make([]byte, 64<<10), int(MaxFileBytes))
	for scan.Scan() {
		var row map[string]any
		if json.Unmarshal(scan.Bytes(), &row) != nil {
			continue
		}
		message, _ := row["message"].(map[string]any)
		if payload, ok := row["payload"].(map[string]any); ok {
			message = payload
		}
		if message == nil {
			message = row
		}
		role, _ := message["role"].(string)
		if role == "" {
			role, _ = message["Role"].(string)
		}
		if role == "human" {
			role = "user"
		}
		if role != "user" && role != "assistant" {
			continue
		}
		if _, ok := message["content"]; !ok {
			message["content"] = message["Parts"]
			if message["content"] == nil {
				message["content"] = message["parts"]
			}
		}
		var parts []string
		switch content := message["content"].(type) {
		case string:
			parts = append(parts, content)
		case []any:
			for _, item := range content {
				if p, ok := item.(map[string]any); ok {
					if text, ok := p["text"].(string); ok {
						parts = append(parts, text)
					} else if text, ok := p["Text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n"))
		if text != "" {
			if len(text) > 12000 {
				text = text[:12000] + "\n[Long message retained in raw history]"
			}
			messages = append(messages, role+": "+text)
		}
	}
	var out strings.Builder
	out.WriteString("# Handoff from the published chat\n\nThis is historical context, not a request to replay tools or continue unfinished effects. The complete raw chat is saved privately for Builders. Native chat resume was not attempted.\n\n")
	if len(messages) == 0 {
		out.WriteString("The raw session has no supported text messages. Ask the owner for the objective; the full raw archive is preserved.\n")
		return out.String()
	}
	out.WriteString("## Starting request\n\n" + messages[0] + "\n\n## Recent decisions, results and unfinished work\n\n")
	start := max(1, len(messages)-40)
	for ; start < len(messages) && len(strings.Join(messages[start:], "\n\n")) > 120000; start++ {
	}
	out.WriteString(strings.Join(messages[start:], "\n\n"))
	return out.String()
}

// Unpack accepts only the declared, checksummed regular files. Call it into a fresh
// revision directory; the caller activates that revision only after this succeeds.
func Unpack(ctx context.Context, input io.Reader, sourceDir, historyDir string) (Manifest, error) {
	return unpack(ctx, input, sourceDir, historyDir, false)
}
func UnpackFiles(ctx context.Context, input io.Reader, sourceDir, historyDir string) (Manifest, error) {
	return unpack(ctx, input, sourceDir, historyDir, true)
}
func unpack(ctx context.Context, input io.Reader, sourceDir, historyDir string, filesOnly bool) (Manifest, error) {
	var manifest Manifest
	tr := tar.NewReader(io.LimitReader(input, MaxBytes+1))
	header, err := tr.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size > 16<<20 {
		return manifest, fmt.Errorf("publication manifest must be first")
	}
	if err = json.NewDecoder(tr).Decode(&manifest); err != nil {
		return manifest, err
	}
	if !filesOnly {
		err = manifest.Validate()
	} else {
		err = manifest.validateFiles()
	}
	if err != nil {
		return manifest, err
	}
	if err = os.MkdirAll(sourceDir, 0750); err != nil {
		return manifest, err
	}
	if err = os.MkdirAll(historyDir, 0700); err != nil {
		return manifest, err
	}
	source, err := os.OpenRoot(sourceDir)
	if err != nil {
		return manifest, err
	}
	defer source.Close()
	history, err := os.OpenRoot(historyDir)
	if err != nil {
		return manifest, err
	}
	defer history.Close()
	expected := map[string]File{}
	if !filesOnly {
		expected["history/raw.jsonl"] = File{Path: "raw.jsonl", Size: manifest.History.CapturedBytes, SHA256: manifest.History.SHA256}
	}
	for _, f := range manifest.Files {
		expected["files/"+f.Path] = f
	}
	for {
		if err = ctx.Err(); err != nil {
			return manifest, err
		}
		header, err = tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return manifest, err
		}
		file, ok := expected[header.Name]
		if !ok || header.Typeflag != tar.TypeReg || header.Size != file.Size {
			return manifest, fmt.Errorf("unexpected publication entry %q", header.Name)
		}
		delete(expected, header.Name)
		dest := source
		if header.Name == "history/raw.jsonl" {
			dest = history
		}
		if err = dest.MkdirAll(path.Dir(file.Path), 0750); err != nil {
			return manifest, err
		}
		mode := os.FileMode(0640)
		if file.Executable {
			mode = 0750
		}
		f, openErr := dest.OpenFile(file.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if openErr != nil {
			return manifest, openErr
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(f, h), tr)
		closeErr := f.Close()
		if copyErr != nil {
			return manifest, copyErr
		}
		if closeErr != nil {
			return manifest, closeErr
		}
		if n != file.Size || hex.EncodeToString(h.Sum(nil)) != file.SHA256 {
			return manifest, fmt.Errorf("checksum mismatch for %q", header.Name)
		}
	}
	if len(expected) != 0 {
		return manifest, fmt.Errorf("publication is incomplete")
	}
	if err = os.WriteFile(filepath.Join(historyDir, "handoff.md"), []byte(manifest.Handoff), 0600); err != nil {
		return manifest, err
	}
	return manifest, nil
}
