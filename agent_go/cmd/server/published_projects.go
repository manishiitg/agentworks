package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

const publishedSourceNotice = "This Crew was published from its owner's computer. You can inspect it, answer questions and save working results here. To change its source files, instructions, skills, functions or dashboard, publish again from that computer. Native chat resume was not attempted; Builder context comes from a handoff summary."

// This record is outside workspace-docs, not an editable project manifest.
// A laptop project ID locates a target only within the authenticated owner's records.
type publishedProject struct {
	WorkspaceID  string    `json:"workspace_id"`
	ProfileID    string    `json:"profile_id"`
	Root         string    `json:"root"`
	OwnerID      string    `json:"owner_id"`
	ProjectID    string    `json:"project_id"`
	Revision     string    `json:"revision"`
	Provider     string    `json:"provider"`
	ModelID      string    `json:"model_id"`
	ConnectionID string    `json:"connection_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type publicationExecutionKey struct{}

func publicationStateRoot() (string, error) {
	base, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "crew-publications"), nil
}

func publicationRecordPath(root string) (string, error) {
	state, err := publicationStateRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonicalCrewWorkspaceRoot(root)))
	return filepath.Join(state, "projects", hex.EncodeToString(sum[:])+".json"), nil
}

func readPublishedProject(ctx context.Context, root string) (*publishedProject, error) {
	ref, ok := resolveCrewPath(ctx, GetUserIDFromContext(ctx), root)
	if !ok {
		return nil, nil
	}
	file, err := publicationRecordPath(ref.Root)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record publishedProject
	if json.Unmarshal(raw, &record) != nil || canonicalCrewWorkspaceRoot(record.Root) != canonicalCrewWorkspaceRoot(ref.Root) || record.OwnerID == "" || record.Revision == "" || strings.ContainsAny(record.Revision, "/\\.") || resolveProjectOwner(ctx, ref.Root) != record.OwnerID {
		return nil, fmt.Errorf("cannot verify this Crew's publication record")
	}
	if pinned, _ := ctx.Value(publicationExecutionKey{}).(*publishedProject); pinned != nil && pinned.Root == record.Root && pinned.OwnerID == record.OwnerID {
		copy := *pinned
		return &copy, nil
	}
	return &record, nil
}

func (p *publishedProject) sourceRoot() string {
	return p.Root + "/.published/" + p.Revision + "/source"
}

func (p *publishedProject) historyDir() (string, error) {
	state, err := publicationStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "history", p.Revision), nil
}

// These are runtime categories, never additional per-person grants. Run turns
// continue to receive only their existing caller/call output scope.
func publishedRuntimeRelative(rel string) bool {
	rel = strings.Trim(rel, "/")
	for _, prefix := range []string{"db", "assets", "reports", "tool_output_folder", ".calls", "shared", "outputs"} {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

func publishedRuntimeRoots(root string) []string {
	var paths []string
	for _, rel := range []string{"db", "assets", "reports", "tool_output_folder", ".calls", "shared", "outputs"} {
		paths = append(paths, root+"/"+rel+"/")
	}
	return paths
}

// Source mutations use the same decision at proxy, MCP, native and function surfaces.
func publishedWriteRefusal(ctx context.Context, raw string) string {
	ref, ok := resolveCrewPath(ctx, GetUserIDFromContext(ctx), raw)
	if !ok {
		return ""
	}
	record, err := readPublishedProject(ctx, ref.Root)
	if err != nil {
		return "Cannot verify this Crew's published source. Try again or ask its owner to check publication status."
	}
	if record == nil {
		return ""
	}
	if strings.HasPrefix(ref.Rest, ".published/") {
		return "To change this Crew's source, publish again from your computer."
	}
	if publishedRuntimeRelative(ref.Rest) {
		return ""
	}
	// Platform-owned metadata and history keep their existing role-checked services;
	// ordinary file routes already protect these records independently.
	if ref.Rest == "product.json" || ref.Rest == "workflow.json" || ref.Rest == "project.json" || strings.HasPrefix(ref.Rest, "builder/") {
		return ""
	}
	return "To change this Crew's source, publish again from your computer. You can still save working results in its runtime folders."
}

// Published file reads select one immutable source revision. Metadata and live
// results stay at the stable project root. Session working-dir pins an in-flight turn.
func publishedReadPath(ctx context.Context, raw string) string {
	ref, ok := resolveCrewPath(ctx, GetUserIDFromContext(ctx), raw)
	if !ok || ref.Rest == "" || strings.HasPrefix(ref.Rest, ".published/") || publishedRuntimeRelative(ref.Rest) {
		return raw
	}
	switch ref.Rest {
	case "product.json", "workflow.json", "project.json":
		return raw
	}
	if strings.HasPrefix(ref.Rest, "builder/") {
		return raw
	}
	record, err := readPublishedProject(ctx, ref.Root)
	if err != nil || record == nil {
		return raw
	}
	source := record.sourceRoot()
	if cfg := common.GetSessionShellConfig(chatSessionIDFromContext(ctx)); cfg != nil && strings.HasPrefix(cfg.WorkingDir, record.Root+"/.published/") {
		source = strings.TrimSuffix(cfg.WorkingDir, "/")
	}
	return path.Join(source, ref.Rest)
}

func publicationSourceForProfile(profile *resolvedAgentProfile) string {
	if profile != nil && profile.Publication != nil {
		return profile.Publication.sourceRoot()
	}
	return ""
}

func publishedBuilderHandoff(req *QueryRequest, readOnly bool) bool {
	if req == nil || readOnly || req.PinRunMode || req.CrewAskReadOnly || req.IsAutoNotification || req.ParentSessionID != "" || req.BotPlatform != "" || req.SessionKind != "" {
		return false
	}
	return req.TriggeredBy == "" || req.TriggeredBy == "interactive"
}

func publicationHistorySummary(record *publishedProject) string {
	if record == nil {
		return ""
	}
	dir, err := record.historyDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		return ""
	}
	return string(raw)
}

func savePublishedProject(record *publishedProject) error {
	file, err := publicationRecordPath(record.Root)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(file, record, 0600)
}
