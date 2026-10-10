package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnsureDefaultFolder gives a deployment a first folder members can use. Brain grants access per person and per folder and an
// empty Brain has no folder, so a member who is not an administrator could not save anything. It creates the top-level folder
// `name` as the administrator when it is absent, and offers `role` (Reader or Editor) on it to every active person once: a person
// whose access an Owner later revokes is not given it again, and people added later are offered it the next time this runs.
// users is the platform's current identity list (the same one SyncPlatformIdentities took); a repeat call with the same list
// does nothing.
func (s *Service) EnsureDefaultFolder(ctx context.Context, admin Principal, name, role string, users []Identity) error {
	if !admin.IsAdmin || admin.IdentityID == "" {
		return kbErr("FORBIDDEN", "An administrator is required.")
	}
	if !validName(name, false) {
		return badArg("Invalid folder name.")
	}
	if r := roleNum(role); r != roleReader && r != roleEditor {
		return badArg("The default role must be Reader or Editor.")
	}
	wanted := []string{}
	for _, u := range users {
		if u.ID != "" && u.ID != admin.IdentityID && !u.Disabled && u.Type != "service" {
			wanted = append(wanted, u.ID)
		}
	}
	sort.Strings(wanted)
	signature := name + "\x00" + role + "\x00" + strings.Join(wanted, ",")
	s.defaultFolder.Lock()
	defer s.defaultFolder.Unlock()
	if s.defaultFolderSignature == signature {
		return nil
	}

	_, err := s.Call(ctx, admin, "create_knowledgebase_folder", map[string]any{"folder_path": "", "name": name, "request_id": "default-folder-" + name})
	var kbe *Error
	if err != nil && !(errors.As(err, &kbe) && kbe.Code == "NAME_CONFLICT") {
		return err
	}
	offered := s.defaultFolderOffered(name)
	info, err := s.Call(ctx, admin, "get_knowledgebase_access", map[string]any{"folder_path": name})
	if err != nil {
		return err
	}
	version := stringArg(asMap(info), "acl_version")
	for _, id := range wanted {
		if offered[id] {
			continue
		}
		granted, err := s.Call(ctx, admin, "manage_knowledgebase_access", map[string]any{
			"action": "grant", "folder_path": name, "identity_id": id, "role": role,
			"expected_acl_version": version, "request_id": "default-grant-" + safeRequestID(name+"-"+id),
		})
		if err != nil {
			return err
		}
		version = stringArg(asMap(granted), "acl_version")
		offered[id] = true
		if err = s.saveDefaultFolderOffered(name, offered); err != nil {
			return err
		}
	}
	s.defaultFolderSignature = signature
	return nil
}

func safeRequestID(v string) string {
	out := []rune{}
	for _, c := range v {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return string(out)
}

func (s *Service) defaultFolderOfferedPath(name string) string {
	return filepath.Join(s.private, "default-folder-"+safeRequestID(name)+".json")
}

// defaultFolderOffered lists the people the default folder was already offered to.
func (s *Service) defaultFolderOffered(name string) map[string]bool {
	offered := map[string]bool{}
	if b, err := os.ReadFile(s.defaultFolderOfferedPath(name)); err == nil {
		var ids []string
		if json.Unmarshal(b, &ids) == nil {
			for _, id := range ids {
				offered[id] = true
			}
		}
	}
	return offered
}

func (s *Service) saveDefaultFolderOffered(name string, offered map[string]bool) error {
	ids := make([]string, 0, len(offered))
	for id := range offered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	path := s.defaultFolderOfferedPath(name)
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
