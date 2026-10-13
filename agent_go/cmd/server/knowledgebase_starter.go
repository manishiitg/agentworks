package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"golang.org/x/sys/unix"
)

type brainStarterCaller interface {
	CallTool(context.Context, knowledgebase.Principal, string, map[string]any) (any, error)
}

type brainStarterFile struct {
	Path, Title, Content, SourceVersion, Package string
}

type brainStarterOffer struct {
	SourceVersion string `json:"source_version"`
	Fingerprint   string `json:"sha256"`
	State         string `json:"state"`
}

func knowledgebaseStarterFiles() []brainStarterFile {
	selection := productpolicy.Selection{}
	guide := productguide.Guide(selection)
	const packagePath = "Shared/Skills/agentworks-mcp"
	return []brainStarterFile{
		{Path: "Shared/AgentWorks/product-guide.md", Title: "AgentWorks product guide", Content: guide, SourceVersion: productguide.GuideVersion},
		{Path: packagePath + "/SKILL.md", Title: "Use AgentWorks over MCP", Content: productguide.Skill(selection), SourceVersion: productguide.SkillVersion, Package: packagePath},
		{Path: packagePath + "/references/products.md", Title: "AgentWorks products", Content: guide, SourceVersion: productguide.GuideVersion, Package: packagePath},
		{Path: packagePath + "/references/versions.json", Title: "AgentWorks guide and skill versions", Content: productguide.Versions(selection), SourceVersion: productguide.SkillVersion, Package: packagePath},
	}
}

func seedKnowledgebaseStarter(ctx context.Context, service brainStarterCaller, admin knowledgebase.Principal) error {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return err
	}
	config, err := knowledgebaseConfig()
	if err != nil {
		return err
	}
	// Platform bootstrap state is independent of Brain's private storage.
	key := productguide.Fingerprint(config.Root + "\x00" + config.LiveRoot + "\x00" + config.OrganizationID)
	return seedBrainStarterFiles(ctx, service, admin, filepath.Join(state, "brain-starters", key), knowledgebaseStarterFiles())
}

// Uses Brain's public tools, retaining native versions, grants and history.
// An offer is durable before creation: an ambiguous failure or crash never
// automatically recreates a note that a person may have subsequently deleted.
// Ambiguous pending offers require explicit operator reconciliation.
func seedBrainStarterFiles(ctx context.Context, service brainStarterCaller, admin knowledgebase.Principal, stateDir string, files []brainStarterFile) error {
	if !admin.IsAdmin || admin.IdentityID == "" || admin.Caps != nil || admin.AccessOnly {
		return errors.New("Brain starter content requires an unrestricted administrator")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, "seed.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	ledger := filepath.Join(stateDir, "offers.json")
	offered := map[string]brainStarterOffer{}
	if b, err := os.ReadFile(ledger); err == nil {
		if err := json.Unmarshal(b, &offered); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	preserved := map[string]bool{}
	for _, file := range files {
		if file.Package == "" {
			continue
		}
		owned := false
		for name, record := range offered {
			owned = owned || record.State == "created" && strings.HasPrefix(name, file.Package+"/")
		}
		if !owned {
			_, err := service.CallTool(ctx, admin, "brain_browse", map[string]any{"action": "folders", "folder_path": file.Package})
			if err == nil {
				preserved[file.Package] = true
			} else if !brainStarterError(err, "NOT_FOUND") {
				return err
			}
		}
	}
	for _, file := range files {
		if _, exists := offered[file.Path]; exists {
			continue
		}
		record := brainStarterOffer{SourceVersion: file.SourceVersion, Fingerprint: productguide.Fingerprint(file.Content), State: "preserved"}
		if preserved[file.Package] {
			offered[file.Path] = record
			if err := writeBrainStarterOffers(ledger, offered); err != nil {
				return err
			}
			continue
		}
		if err := ensureBrainStarterParents(ctx, service, admin, path.Dir(file.Path)); err != nil {
			return err
		}
		record.State = "offered"
		offered[file.Path] = record
		if err := writeBrainStarterOffers(ledger, offered); err != nil {
			return err
		}
		kind := "note"
		if path.Base(file.Path) == "SKILL.md" {
			kind = "skill"
		}
		_, err := service.CallTool(ctx, admin, "brain_update", map[string]any{
			"action": "create", "folder_path": path.Dir(file.Path), "filename": path.Base(file.Path),
			"type": kind, "title": file.Title, "content": file.Content,
			"request_id": "platform-starter-" + productguide.Fingerprint(file.Path)[:32],
		})
		if err != nil && !brainStarterError(err, "NAME_CONFLICT") {
			if brainStarterError(err, "REQUEST_IN_PROGRESS") {
				// The native lock refused this operation before any mutation.
				delete(offered, file.Path)
				if saveErr := writeBrainStarterOffers(ledger, offered); saveErr != nil {
					return saveErr
				}
			}
			return err
		}
		if err == nil {
			record.State = "created"
		} else {
			record.State = "preserved"
		}
		offered[file.Path] = record
		if err := writeBrainStarterOffers(ledger, offered); err != nil {
			return err
		}
	}
	return nil
}

func brainStarterError(err error, code string) bool {
	var failure *knowledgebase.Error
	return errors.As(err, &failure) && failure.Code == code
}

func ensureBrainStarterParents(ctx context.Context, service brainStarterCaller, admin knowledgebase.Principal, folder string) error {
	parent := "Shared"
	for _, name := range strings.Split(strings.TrimPrefix(folder, "Shared/"), "/") {
		if folder == "Shared" {
			break
		}
		next := parent + "/" + name
		_, err := service.CallTool(ctx, admin, "brain_browse", map[string]any{"action": "folders", "folder_path": next})
		if err != nil {
			if !brainStarterError(err, "NOT_FOUND") {
				return err
			}
			_, err = service.CallTool(ctx, admin, "brain_update", map[string]any{"action": "create_folder", "folder_path": parent, "name": name, "request_id": "platform-starter-folder-" + productguide.Fingerprint(next)[:32]})
			if err != nil && !brainStarterError(err, "NAME_CONFLICT") {
				return err
			}
		}
		parent = next
	}
	return nil
}

func writeBrainStarterOffers(file string, offered map[string]brainStarterOffer) error {
	b, err := json.Marshal(offered)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".offers-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
