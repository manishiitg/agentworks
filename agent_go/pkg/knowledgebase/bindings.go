package knowledgebase

import (
	"context"
	"regexp"
	"strings"
)

// Binding is an additional restriction, never a grant. IDs survive folder
// display-name changes; no shared binding exposes a host filesystem path.
type Binding struct {
	Alias    string `json:"alias"`
	FolderID string `json:"folder_id"`
	Access   string `json:"access"`
}

type BindingPolicy struct {
	Bindings []Binding
	Audience []string
}

var bindingAlias = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

func ValidateBindings(bindings []Binding, reserved []string) error {
	if len(bindings) > 20 {
		return badArg("At most 20 shared Knowledge Base bindings are allowed.")
	}
	seen := map[string]bool{}
	for _, alias := range reserved {
		seen[alias] = true
	}
	for _, b := range bindings {
		if !bindingAlias.MatchString(b.Alias) || b.Alias == "access" || seen[b.Alias] || !strings.HasPrefix(b.FolderID, "folder_") || strings.ContainsAny(b.FolderID, "/\\.") || b.Access != "read" && b.Access != "write" {
			return badArg("Bindings require a unique alias, immutable folder ID, and read or write access.")
		}
		seen[b.Alias] = true
	}
	return nil
}

// ResolveBinding authorizes the execution principal and every output reader
// under the same content lock. Platform administrator status is deliberately
// not inherited by members of the output audience.
func (s *Service) ResolveBinding(ctx context.Context, p Principal, b Binding, audience []string) (string, error) {
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := ValidateBindings([]Binding{b}, nil); err != nil {
		return "", err
	}
	f, err := s.findFolder("", b.FolderID)
	if err != nil {
		return "", err
	}
	if !s.IdentityActive(ctx, p.IdentityID) || s.effective(p, f.Path) < roleReader {
		return "", kbErr("NOT_FOUND", "Shared Knowledge Base binding is unavailable.")
	}
	if b.Access == "write" && s.effective(p, f.Path) < roleEditor {
		return "", kbErr("FORBIDDEN", "The binding requires Editor access.")
	}
	if len(audience) == 0 {
		return "", kbErr("FORBIDDEN", "Shared knowledge requires an explicit output audience.")
	}
	for _, id := range audience {
		if !s.IdentityActive(ctx, id) || s.effective(Principal{IdentityID: id}, f.Path) < roleReader {
			return "", kbErr("FORBIDDEN", "An output audience member cannot read the shared folder.")
		}
	}
	return f.Path, nil
}

func (s *Service) boundRole(p Principal, folder string) int {
	policy := p.BindingPolicy
	if policy == nil {
		return roleOwner
	}
	if len(policy.Audience) == 0 {
		return 0
	}
	for _, id := range policy.Audience {
		if !s.IdentityActive(context.Background(), id) || s.effectiveRaw(Principal{IdentityID: id}, folder) < roleReader {
			return 0
		}
	}
	role := 0
	for _, b := range policy.Bindings {
		f, err := s.findFolder("", b.FolderID)
		if err != nil || !within(folder, f.Path) {
			continue
		}
		n := roleReader
		if b.Access == "write" {
			n = roleEditor
		}
		if n > role {
			role = n
		}
	}
	return role
}

func ValidateImportPath(path string) error { return validatePath(path, true) }

func (s *Service) RequireIdentityFolderRole(ctx context.Context, id, folderID, role string) error {
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := s.findFolder("", folderID)
	if err != nil {
		return err
	}
	n := roleNum(role)
	if n == 0 || !s.IdentityActive(ctx, id) || s.effectiveRaw(Principal{IdentityID: id}, f.Path) < n {
		return kbErr("FORBIDDEN", "A required migration grant is missing.")
	}
	return nil
}
func NormalizeImportText(content string) (string, error) {
	return normalizeText(content, 10*1024*1024, "Content")
}
