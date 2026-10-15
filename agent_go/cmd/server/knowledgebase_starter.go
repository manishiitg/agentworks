package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	SourceVersion string             `json:"source_version"`
	Fingerprint   string             `json:"sha256"`
	State         string             `json:"state"`
	Revision      string             `json:"revision,omitempty"`
	Previous      *brainStarterOffer `json:"previous,omitempty"`
}

var brainStarterCache struct {
	sync.Mutex
	key string
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
		{Path: "Shared/AgentWorks/cli-setup.md", Title: "AgentWorks CLI setup", Content: productguide.CLISetup(selection), SourceVersion: productguide.CLISetupVersion},
		{Path: packagePath + "/references/cli-setup.md", Title: "AgentWorks CLI setup", Content: productguide.CLISetup(selection), SourceVersion: productguide.CLISetupVersion, Package: packagePath},
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
	files := knowledgebaseStarterFiles()
	b, _ := json.Marshal(files)
	cacheKey := fmt.Sprintf("%p:%s:%s", service, key, productguide.Fingerprint(string(b)))
	brainStarterCache.Lock()
	defer brainStarterCache.Unlock()
	if brainStarterCache.key == cacheKey {
		return nil
	}
	if err := seedBrainStarterFiles(ctx, service, admin, filepath.Join(state, "brain-starters", key), files); err != nil {
		return err
	}
	brainStarterCache.key = cacheKey
	return nil
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
	if offered == nil {
		return errors.New("invalid Brain starter offer state")
	}
	// Recover known effects before considering any new write. A missing file
	// after an ambiguous create is never recreated: it may have been deleted.
	pending := false
	for _, file := range files {
		record, exists := offered[file.Path]
		if !exists || record.State != "offered" {
			continue
		}
		live, found, err := readBrainStarter(ctx, service, admin, file.Path)
		if err != nil {
			return err
		}
		switch {
		case found && record.Previous != nil && live.Fingerprint == record.Previous.Fingerprint && live.Revision == record.Previous.Revision:
			record = *record.Previous
			log.Printf("[BRAIN] starter %s update had no visible effect; retrying against the unchanged revision", file.Path)
		case found || record.Previous != nil:
			record.State, record.Previous = "preserved", nil
			log.Printf("[BRAIN] starter %s reconciled after an ambiguous write; preserving current content or deletion", file.Path)
		default:
			pending = true
			log.Printf("[BRAIN] starter %s is still missing after an ambiguous create; explicit reconciliation required", file.Path)
		}
		offered[file.Path] = record
		if err := writeBrainStarterOffers(ledger, offered); err != nil {
			return err
		}
	}
	preserved := map[string]bool{}
	for _, file := range files {
		if file.Package == "" {
			continue
		}
		if _, checked := preserved[file.Package]; checked {
			continue
		}
		owned := false
		for name, record := range offered {
			owned = owned || (record.State == "created" || record.State == "offered") && strings.HasPrefix(name, file.Package+"/")
		}
		preserved[file.Package] = false
		if !owned {
			_, err := service.CallTool(ctx, admin, "brain_browse", map[string]any{"action": "folders", "folder_path": file.Package})
			if err == nil {
				preserved[file.Package] = true
			} else if !brainStarterError(err, "NOT_FOUND") {
				return err
			}
		} else {
			// A customized or deleted file makes the whole skill user-owned.
			for _, member := range files {
				if member.Package != file.Package {
					continue
				}
				record, seen := offered[member.Path]
				if !seen || record.State == "offered" {
					continue
				}
				if record.State == "preserved" {
					preserved[file.Package] = true
					break
				}
				live, found, err := readBrainStarter(ctx, service, admin, member.Path)
				if err != nil {
					return err
				}
				if !found || live.Fingerprint != record.Fingerprint || record.Revision != "" && live.Revision != record.Revision {
					preserved[file.Package] = true
					break
				}
			}
		}
	}
	for _, file := range files {
		previous, exists := offered[file.Path]
		if exists && (previous.State == "preserved" || previous.State == "offered") {
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
		action := "create"
		args := map[string]any{"action": action, "folder_path": path.Dir(file.Path), "filename": path.Base(file.Path), "type": "note", "title": file.Title, "content": file.Content}
		if !exists {
			_, found, err := readBrainStarter(ctx, service, admin, file.Path)
			if err != nil {
				return err
			}
			if found {
				offered[file.Path] = record
				if err := writeBrainStarterOffers(ledger, offered); err != nil {
					return err
				}
				continue
			}
		}
		if exists {
			if !starterVersionCanRefresh(file.SourceVersion, previous.SourceVersion) {
				continue
			}
			if previous.Fingerprint == record.Fingerprint && previous.SourceVersion == record.SourceVersion {
				continue
			}
			live, found, err := readBrainStarter(ctx, service, admin, file.Path)
			if err != nil {
				return err
			}
			if !found || live.Fingerprint != previous.Fingerprint || previous.Revision != "" && live.Revision != previous.Revision {
				previous.State = "preserved"
				offered[file.Path] = previous
				if err := writeBrainStarterOffers(ledger, offered); err != nil {
					return err
				}
				continue
			}
			action = "update"
			args = map[string]any{"action": action, "path": file.Path, "content": file.Content, "expected_version": live.Revision}
			copy := previous
			record.Previous = &copy
		}
		if err := ensureBrainStarterParents(ctx, service, admin, path.Dir(file.Path)); err != nil {
			return err
		}
		record.State = "offered"
		offered[file.Path] = record
		if err := writeBrainStarterOffers(ledger, offered); err != nil {
			return err
		}
		if path.Base(file.Path) == "SKILL.md" {
			if action == "create" {
				args["type"] = "skill"
			}
		}
		args["request_id"] = "platform-starter-" + productguide.Fingerprint(file.Path + "\x00" + record.Fingerprint + "\x00" + action + "\x00" + fmt.Sprint(args["expected_version"]))[:32]
		result, err := service.CallTool(ctx, admin, "brain_update", args)
		if err != nil && !brainStarterError(err, "NAME_CONFLICT") {
			if definiteBrainStarterRefusal(err) {
				if exists {
					offered[file.Path] = previous
				} else {
					delete(offered, file.Path)
				}
				if saveErr := writeBrainStarterOffers(ledger, offered); saveErr != nil {
					return saveErr
				}
			}
			return fmt.Errorf("Brain starter %s mutation did not complete: %w", file.Path, err)
		}
		if err == nil {
			record.State = "created"
			b, _ := json.Marshal(result)
			var saved struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(b, &saved); err != nil {
				return err
			}
			record.Revision = saved.Version
			if record.Revision == "" {
				return fmt.Errorf("Brain starter %s mutation returned no revision", file.Path)
			}
		} else {
			record.State = "preserved"
		}
		record.Previous = nil
		offered[file.Path] = record
		if err := writeBrainStarterOffers(ledger, offered); err != nil {
			return err
		}
	}
	if pending {
		return errors.New("Brain starter creation needs explicit reconciliation; see logged paths")
	}
	return nil
}

type brainStarterSnapshot struct{ Fingerprint, Revision string }

func readBrainStarter(ctx context.Context, service brainStarterCaller, admin knowledgebase.Principal, file string) (brainStarterSnapshot, bool, error) {
	result, err := service.CallTool(ctx, admin, "brain_read", map[string]any{"action": "read", "path": file})
	if brainStarterError(err, "NOT_FOUND") {
		return brainStarterSnapshot{}, false, nil
	}
	if err != nil {
		return brainStarterSnapshot{}, false, err
	}
	b, err := json.Marshal(result)
	if err != nil {
		return brainStarterSnapshot{}, false, err
	}
	var live struct {
		Content string `json:"content"`
		Version string `json:"version"`
		Binary  bool   `json:"binary"`
	}
	if err := json.Unmarshal(b, &live); err != nil {
		return brainStarterSnapshot{}, false, err
	}
	if live.Version == "" {
		return brainStarterSnapshot{}, false, errors.New("Brain starter read has no revision")
	}
	fingerprint := ""
	if !live.Binary {
		fingerprint = productguide.Fingerprint(live.Content)
	}
	return brainStarterSnapshot{Fingerprint: fingerprint, Revision: live.Version}, true, nil
}

func definiteBrainStarterRefusal(err error) bool {
	var failure *knowledgebase.Error
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code {
	case "FORBIDDEN", "UNAUTHENTICATED", "INVALID_ARGUMENT", "LIMIT_EXCEEDED", "REQUEST_IN_PROGRESS", "VERSION_CONFLICT", "NOT_FOUND", "REQUEST_ID_REUSE":
		return true
	}
	return false
}

// Equal source versions can have different product projections. An older
// server must not downgrade an untouched copy from a newer source release.
func starterVersionCanRefresh(source, installed string) bool {
	a, b := strings.Split(source, "."), strings.Split(installed, ".")
	if len(a) != 3 || len(b) != 3 {
		return false
	}
	for i := range a {
		left, e1 := strconv.Atoi(a[i])
		right, e2 := strconv.Atoi(b[i])
		if e1 != nil || e2 != nil || left < 0 || right < 0 {
			return false
		}
		if left != right {
			return left > right
		}
	}
	return true
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
