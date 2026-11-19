package server

// PLAT-746-1023: an item-only package over the existing chunk format. A package
// is private, short lived and bound to the authenticated source operation. No
// request ever supplies an operating-system path or a source capability.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

type teamOnlineRequest struct {
	TeamProjectID      string   `json:"team_project_id"`
	SourceProjectID    string   `json:"source_project_id"`
	Kind               string   `json:"kind"`
	ItemID             string   `json:"item_id"`
	Revision           string   `json:"item_revision"`
	SubmissionID       string   `json:"submission_id"`
	Preview            bool     `json:"preview"`
	IncludeAutomations []string `json:"include_automations,omitempty"`
}

type teamOnlinePackageManifest struct {
	SourceProjectID string                 `json:"source_project_id"`
	Files           map[string]chunks.File `json:"files"`
	Token           string                 `json:"token,omitempty"`
}

type teamOnlinePackage struct {
	mu             sync.Mutex
	root           *os.Root
	dir            string
	request        teamOnlineRequest
	owner          string
	manifest       teamOnlinePackageManifest
	chunkRoot      *os.Root
	expires        time.Time
	desiredEnabled map[string]bool
	sourceLocal    triggerTarget
	sourceStopped  bool
	// Set on the receiving side before the source removes a moved workflow.
	moved *teamOnlineMovedWorkflow
}

func newTeamOnlinePackage(project string) (*teamOnlinePackage, error) {
	parent, err := crewSyncStagingRoot(project)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "team-item-")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &teamOnlinePackage{root: root, dir: dir, expires: time.Now().Add(publication.TransferTimeout)}, nil
}
func (p *teamOnlinePackage) close() {
	if p.chunkRoot != nil {
		p.chunkRoot.Close()
	}
	if p.root != nil {
		p.root.Close()
	}
	// dir is made by MkdirTemp above, never by a request.
	if p.dir != "" {
		_ = os.RemoveAll(p.dir)
	}
}

func (p *teamOnlinePackage) index() error {
	p.manifest.Files = map[string]chunks.File{}
	if err := p.root.Mkdir(".chunks", 0700); err != nil {
		return err
	}
	var err error
	p.chunkRoot, err = p.root.OpenRoot(".chunks")
	if err != nil {
		return err
	}
	var total int64
	err = fs.WalkDir(p.root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == ".chunks" {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if _, err := publication.CleanPath(name); err != nil || publication.CredentialPath(name) {
			return fmt.Errorf("the item contains a credential or unsafe file")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
			return fmt.Errorf("the item exceeds the normal publication file limits")
		}
		total += info.Size()
		if total > publication.MaxBytes || len(p.manifest.Files) >= publication.MaxFiles {
			return fmt.Errorf("the item exceeds the normal publication limits")
		}
		f, err := p.root.Open(name)
		if err != nil {
			return err
		}
		version, err := chunks.Split(f, info.Size(), false, func(id string, data []byte) error {
			out, err := p.chunkRoot.OpenFile(id, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if os.IsExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			_, err = out.Write(data)
			ce := out.Close()
			if err != nil {
				return err
			}
			return ce
		})
		f.Close()
		if err != nil {
			return err
		}
		p.manifest.Files[name] = version
		return nil
	})
	return err
}

// The scanner is built at the source too: private secret values never travel
// to the receiver merely so it can look for them.
func (api *StreamingAPI) scanTeamOnlinePackage(ctx context.Context, source triggerTarget, p *teamOnlinePackage) error {
	selection, err := projectSecretSelection(ctx, source.Path)
	if err != nil {
		return err
	}
	secrets, err := api.projectRunSecrets(ctx, source.CrewOwner, source.Path, &selection)
	if err != nil {
		return err
	}
	matched := false
	contains := func(data []byte) bool {
		for _, s := range secrets {
			if s.Value != "" && bytes.Contains(data, []byte(s.Value)) {
				matched = true
				return true
			}
		}
		return false
	}
	for name := range p.manifest.Files {
		f, err := p.root.Open(name)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, publication.MaxFileBytes+1))
		f.Close()
		if err != nil {
			return err
		}
		if contains(data) {
			return fmt.Errorf("secret_match")
		}
		var decoded any
		if json.Unmarshal(data, &decoded) == nil && teamOnlineDecodedContainsSecret(decoded, contains) {
			return fmt.Errorf("secret_match")
		}
	}
	if p.request.Kind == "dashboard" || p.request.Kind == "artifact" {
		f, err := p.root.Open(teamonline.Descriptor)
		if err != nil {
			return err
		}
		item, err := teamonline.DecodeItem(f)
		f.Close()
		if err != nil {
			return err
		}
		for _, value := range item.Snapshot.Files {
			if contains([]byte(value)) {
				return fmt.Errorf("secret_match")
			}
		}
		if err := teamonline.InspectParts(p.root, item.DatasetParts, contains); err != nil {
			if matched {
				return fmt.Errorf("secret_match")
			}
			return err
		}
	} else {
		f, err := p.root.Open(teamOnlineAutomationDescriptor)
		if err != nil {
			return err
		}
		var item teamOnlineAutomationPackage
		err = json.NewDecoder(f).Decode(&item)
		f.Close()
		if err != nil {
			return err
		}
		decoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if contains(decoded) {
			return fmt.Errorf("secret_match")
		}
		for _, schedule := range item.Schedules {
			var input scheduleExecutionInput
			if err := json.Unmarshal(schedule.Input, &input); err != nil {
				return err
			}
			for _, m := range input.Messages {
				if contains([]byte(m)) {
					return fmt.Errorf("secret_match")
				}
			}
			if input.Condition != nil && contains([]byte(input.Condition.Script)) {
				return fmt.Errorf("secret_match")
			}
		}
	}
	return nil
}

// Reconstruct only declared files, checking both the established chunk hashes
// and whole-file hashes. The fetch callback is an authenticated pair command
// or a local exporter, never an arbitrary URL or caller-selected filesystem.
func receiveTeamOnlinePackage(project string, m teamOnlinePackageManifest, fetch func([]string) ([]byte, error)) (*teamOnlinePackage, error) {
	if len(m.Files) == 0 || len(m.Files) > publication.MaxFiles {
		return nil, fmt.Errorf("invalid item package")
	}
	var total int64
	for name, v := range m.Files {
		if _, err := publication.CleanPath(name); err != nil || publication.CredentialPath(name) || !v.Valid() || v.Size > publication.MaxFileBytes {
			return nil, fmt.Errorf("invalid item package file")
		}
		total += v.Size
		if total > publication.MaxBytes {
			return nil, fmt.Errorf("the item exceeds the normal publication limits")
		}
	}
	p, err := newTeamOnlinePackage(project)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			p.close()
		}
	}()
	for name, v := range m.Files {
		parent := filepath.ToSlash(filepath.Dir(name))
		if parent != "." {
			if err = p.root.MkdirAll(parent, 0700); err != nil {
				return nil, err
			}
		}
		f, e := p.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		hash := sha256.New()
		var written int64
		for _, c := range v.Chunks {
			frames, e := fetch([]string{c.ID})
			if e != nil {
				f.Close()
				return nil, e
			}
			seen := false
			e = chunks.ReadFrames(bytes.NewReader(frames), func(id string, data []byte) error {
				if seen || id != c.ID || int64(len(data)) != c.Len {
					return fmt.Errorf("invalid item chunk")
				}
				sum := sha256.Sum256(data)
				if hex.EncodeToString(sum[:]) != id {
					return fmt.Errorf("invalid item chunk hash")
				}
				seen = true
				n, e := io.MultiWriter(f, hash).Write(data)
				written += int64(n)
				return e
			})
			if e != nil || !seen {
				f.Close()
				return nil, fmt.Errorf("invalid item chunk")
			}
		}
		e = f.Close()
		if e != nil {
			return nil, e
		}
		if written != v.Size || hex.EncodeToString(hash.Sum(nil)) != v.Hash {
			return nil, fmt.Errorf("the item file hash does not match")
		}
	}
	p.manifest = m
	ok = true
	return p, nil
}

var teamOnlineExports sync.Map // random single-use token -> *teamOnlinePackage
func retainTeamOnlineExport(p *teamOnlinePackage) string {
	token := uuid.NewString()
	p.manifest.Token = token
	teamOnlineExports.Store(token, p)
	time.AfterFunc(publication.TransferTimeout, func() {
		if value, ok := teamOnlineExports.LoadAndDelete(token); ok {
			p := value.(*teamOnlinePackage)
			p.mu.Lock()
			defer p.mu.Unlock()
			p.close()
		}
	})
	return token
}

func teamOnlineDecodedContainsSecret(value any, contains func([]byte) bool) bool {
	switch v := value.(type) {
	case string:
		return contains([]byte(v))
	case []any:
		for _, x := range v {
			if teamOnlineDecodedContainsSecret(x, contains) {
				return true
			}
		}
	case map[string]any:
		for k, x := range v {
			if contains([]byte(k)) || teamOnlineDecodedContainsSecret(x, contains) {
				return true
			}
		}
	}
	return false
}
