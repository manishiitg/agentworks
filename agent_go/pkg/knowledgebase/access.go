package knowledgebase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var validStem = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,63}$`)

func validName(name string, file bool) bool {
	stem := name
	if file {
		if !strings.HasSuffix(name, ".md") {
			return false
		}
		stem = strings.TrimSuffix(name, ".md")
	}
	if !validStem.MatchString(stem) || strings.HasSuffix(stem, " ") {
		return false
	}
	u := strings.ToUpper(stem)
	switch u {
	case "CON", "NUL", "AUX", "PRN":
		return false
	}
	if len(u) == 4 && (strings.HasPrefix(u, "COM") || strings.HasPrefix(u, "LPT")) && u[3] >= '1' && u[3] <= '9' {
		return false
	}
	return true
}
func validatePath(p string, file bool) error {
	if len(p) > 1024 || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return badArg("Invalid organization-relative path.")
	}
	if p == "" && !file {
		return nil
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if !validName(part, file && i == len(parts)-1) {
			return badArg("Invalid organization-relative path.")
		}
	}
	return nil
}
func within(child, parent string) bool {
	return parent == "" || strings.EqualFold(child, parent) || strings.HasPrefix(strings.ToLower(child), strings.ToLower(parent)+"/")
}
func roleNum(r string) int {
	switch strings.ToLower(r) {
	case "reader":
		return roleReader
	case "editor":
		return roleEditor
	case "owner":
		return roleOwner
	}
	return 0
}
func roleName(r int) string {
	switch r {
	case roleReader:
		return "Reader"
	case roleEditor:
		return "Editor"
	case roleOwner:
		return "Owner"
	}
	return "None"
}
func (s *Service) identities() (map[string]Identity, error) {
	ids := map[string]Identity{}
	b, e := os.ReadFile(filepath.Join(s.private, "identities.json"))
	if os.IsNotExist(e) {
		return ids, nil
	}
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(b, &ids)
	return ids, e
}
func (s *Service) identityKnown(id string) bool {
	ids, e := s.identities()
	if e != nil {
		return false
	}
	_, ok := ids[id]
	return ok
}
func (s *Service) IdentityActive(ctx context.Context, id string) bool {
	ids, e := s.identities()
	if e != nil {
		return false
	}
	i, ok := ids[id]
	return ok && !i.Disabled
}
func (s *Service) SyncPlatformIdentities(ctx context.Context, users []Identity) error {
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return err
	}
	if err = s.recover(); err != nil {
		unlock()
		return err
	}
	_, changed, err := s.platformIdentityUpdate(users)
	unlock()
	if err != nil || !changed {
		return err
	}
	// Unchanged snapshots need only the content lock. Security mutations always
	// acquire publication before content, matching grant changes and pushes.
	unlockSecurity, err := s.publicationLock()
	if err != nil {
		return err
	}
	defer unlockSecurity()
	unlock, err = s.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	if err = s.recover(); err != nil {
		return err
	}
	// Re-read after acquiring both locks: another sync or service-account change
	// may have completed since the initial comparison.
	ids, changed, err := s.platformIdentityUpdate(users)
	if err != nil || !changed {
		return err
	}
	return s.transact([]fileChange{{Access: &accessChange{OnlyGeneration: true, SecurityGeneration: s.nextSecurityGeneration()}}, jsonChange(filepath.Join(s.private, "identities.json"), ids)})
}

// Called with the content lock held; the returned map is not persisted yet.
func (s *Service) platformIdentityUpdate(users []Identity) (map[string]Identity, bool, error) {
	ids, err := s.identities()
	if err != nil {
		return nil, false, err
	}
	changed := false
	seen := map[string]bool{}
	for _, id := range users {
		if id.ID == "" {
			continue
		}
		seen[id.ID] = true
		if old, exists := ids[id.ID]; exists && old.Type == "service" {
			continue
		}
		id.Type = "user"
		if old, exists := ids[id.ID]; !exists || old != id {
			ids[id.ID] = id
			changed = true
		}
	}
	for id, old := range ids {
		if old.Type == "user" && !seen[id] && !old.Disabled {
			old.Disabled = true
			ids[id] = old
			changed = true
		}
	}
	return ids, changed, nil
}
func (s *Service) ValidateServiceTokenIdentity(ctx context.Context, p Principal, id string) error {
	if !p.IsAdmin || !s.IdentityActive(ctx, p.IdentityID) {
		return kbErr("FORBIDDEN", "An administrator is required to issue service account tokens.")
	}
	ids, e := s.identities()
	if e != nil {
		return e
	}
	i, ok := ids[id]
	if !ok || i.Type != "service" || i.Disabled {
		return kbErr("FORBIDDEN", "Tokens can target only an enabled service account.")
	}
	return nil
}
func (s *Service) ValidateTokenIdentity(ctx context.Context, p Principal, id string) error {
	if !s.IdentityActive(ctx, p.IdentityID) {
		return kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
	}
	if id != p.IdentityID && !p.IsAdmin {
		return kbErr("FORBIDDEN", "Only an administrator can issue tokens for another identity.")
	}
	if !s.IdentityActive(ctx, id) {
		return kbErr("NOT_FOUND", "Resource not found.")
	}
	return nil
}
func (s *Service) ValidateCaps(ctx context.Context, p Principal, id string, caps *[]Cap) error {
	if err := s.ValidateTokenIdentity(ctx, p, id); err != nil {
		return err
	}
	if caps == nil {
		return nil
	}
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	target := Principal{IdentityID: id, IsAdmin: id == p.IdentityID && p.IsAdmin}
	for _, c := range *caps {
		n := roleNum(c.Role)
		if n < roleReader || n > roleEditor {
			return badArg("Connection caps must be Reader or Editor.")
		}
		r, e := s.findFolder(c.FolderPath, "")
		if e != nil {
			return e
		}
		if s.effective(target, r.Path) < n {
			return kbErr("FORBIDDEN", "A connection cap exceeds the identity's current access.")
		}
	}
	return nil
}
func (s *Service) effective(p Principal, folder string) int {
	role := s.effectiveRaw(p, folder)
	if bound := s.boundRole(p, folder); bound < role {
		role = bound
	}
	return role
}
func (s *Service) effectiveRaw(p Principal, folder string) int {
	role := 0
	if p.IsAdmin {
		role = roleOwner
	} else {
		rows, err := s.db.Query(`SELECT folder_path,role FROM grants WHERE identity_id=?`, p.IdentityID)
		if err != nil {
			return 0
		}
		for rows.Next() {
			var f string
			var r int
			if rows.Scan(&f, &r) == nil && within(folder, f) && r > role {
				role = r
			}
		}
		rows.Close()
	}
	if p.Caps != nil {
		cap := 0
		for _, c := range *p.Caps {
			if within(folder, c.FolderPath) && roleNum(c.Role) > cap {
				cap = roleNum(c.Role)
			}
		}
		if cap < role {
			role = cap
		}
	}
	return role
}
func (s *Service) require(p Principal, folder string, role int) error {
	have := s.effective(p, folder)
	if have < roleReader {
		return kbErr("NOT_FOUND", "Resource not found.")
	}
	if have < role {
		return kbErr("FORBIDDEN", "The requested operation requires higher folder access.")
	}
	return nil
}
func (s *Service) findFolder(folder, id string) (folderRegistry, error) {
	if id == "" {
		if err := validatePath(folder, false); err != nil {
			return folderRegistry{}, err
		}
	}
	rs, err := s.registries()
	if err != nil {
		return folderRegistry{}, err
	}
	for _, r := range rs {
		if (id != "" && r.ID == id) || (id == "" && strings.EqualFold(r.Path, folder)) {
			return r, nil
		}
	}
	return folderRegistry{}, kbErr("NOT_FOUND", "Resource not found.")
}
func (s *Service) resolveFolder(p Principal, args map[string]any, role int) (folderRegistry, error) {
	folder, fp := args["folder_path"].(string)
	id, ip := args["folder_id"].(string)
	if fp && ip {
		return folderRegistry{}, badArg("Specify one folder locator.")
	}
	r, err := s.findFolder(folder, id)
	if err != nil {
		return r, err
	}
	if err = s.require(p, r.Path, role); err != nil {
		return folderRegistry{}, err
	}
	return r, nil
}
func (s *Service) resolveEntry(p Principal, args map[string]any, role int) (Entry, folderRegistry, error) {
	id, ip := args["entry_id"].(string)
	pp, ppresent := args["path"].(string)
	if ip == ppresent || ip && id == "" || ppresent && pp == "" {
		return Entry{}, folderRegistry{}, badArg("Specify exactly one entry_id or path.")
	}
	if ppresent {
		if err := validatePath(pp, true); err != nil {
			return Entry{}, folderRegistry{}, err
		}
	}
	rs, err := s.registries()
	if err != nil {
		return Entry{}, folderRegistry{}, err
	}
	for _, r := range rs {
		for _, e := range r.Entries {
			if (ip && e.ID == id) || (ppresent && strings.EqualFold(e.Path, pp)) {
				if err = s.require(p, r.Path, role); err != nil {
					return Entry{}, folderRegistry{}, err
				}
				return e, r, nil
			}
		}
	}
	return Entry{}, folderRegistry{}, kbErr("NOT_FOUND", "Resource not found.")
}
func (s *Service) access(p Principal, args map[string]any) (any, error) {
	r, err := s.resolveFolder(p, args, roleReader)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"folder_id": r.ID, "folder_path": r.Path, "effective_role": roleName(s.effective(p, r.Path)), "role": roleName(s.effective(p, r.Path))}
	var generation int
	s.db.QueryRow(`SELECT value FROM security WHERE key='generation'`).Scan(&generation)
	out["acl_generation"] = generation
	out["acl_version"] = s.aclVersion(r.ID)
	if s.effective(p, r.Path) < roleOwner {
		return out, nil
	}
	ids, err := s.identities()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT identity_id,folder_path,role FROM grants`)
	if err != nil {
		return nil, err
	}
	grants := []any{}
	for rows.Next() {
		var id, folder string
		var role int
		if rows.Scan(&id, &folder, &role) != nil {
			continue
		}
		i, ok := ids[id]
		if !ok {
			continue
		}
		if within(r.Path, folder) {
			grants = append(grants, map[string]any{"identity_id": id, "name": i.Name, "identity_type": i.Type, "disabled": i.Disabled, "folder_path": folder, "role": roleName(role), "inherited": !strings.EqualFold(folder, r.Path)})
		}
	}
	rows.Close()
	out["grants"] = grants
	identities := []Identity{}
	for _, id := range ids {
		identities = append(identities, id)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].Name < identities[j].Name })
	out["identities"] = identities
	return out, nil
}
func (s *Service) manage(ctx context.Context, p Principal, a map[string]any) (any, []fileChange, error) {
	action := stringArg(a, "action")
	if action == "list" {
		v, e := s.accessDiscovery(p, a)
		return v, nil, e
	}
	r, err := s.resolveFolder(p, a, roleOwner)
	if err != nil {
		return nil, nil, err
	}
	target := stringArg(a, "identity_id")
	ids, err := s.identities()
	if err != nil {
		return nil, nil, err
	}
	changes := []fileChange{}
	result := map[string]any{"action": action, "folder_path": r.Path, "identity_id": target}
	security := accessChange{OnlyGeneration: true, SecurityGeneration: s.nextSecurityGeneration()}
	aclSequence := 1
	aclMutation := action == "grant" || action == "revoke"
	if aclMutation {
		if stringArg(a, "expected_acl_version") == "" {
			return nil, nil, badArg("expected_acl_version is required for grant changes.")
		}
		if e := s.db.QueryRowContext(ctx, `SELECT value FROM security WHERE key=?`, "acl:"+r.ID).Scan(&aclSequence); e != nil {
			aclSequence = 1
		}
		version := s.aclToken(r.ID, aclSequence)
		if stringArg(a, "expected_acl_version") != version {
			return nil, nil, &Error{Code: "ACL_VERSION_CONFLICT", Message: "Folder access changed; inspect its current grants and retry.", Details: map[string]any{"current_acl_version": version}}
		}
		security.OnlyGeneration = false
		security.IdentityID = target
		security.FolderPath = r.Path
		security.FolderID = r.ID
		security.ACLSequence = aclSequence + 1
		result["acl_version"] = s.aclToken(r.ID, security.ACLSequence)
	}
	switch action {
	case "grant":
		role := roleNum(stringArg(a, "role"))
		if role == 0 {
			return nil, nil, badArg("role must be Reader, Editor, or Owner.")
		}
		id, ok := ids[target]
		if !ok || id.Disabled {
			return nil, nil, kbErr("NOT_FOUND", "Resource not found.")
		}
		security.Role = role
		result["role"] = roleName(role)
	case "revoke":
		if target == "" {
			return nil, nil, badArg("identity_id is required.")
		}
		security.Revoke = true
	case "create_service_account":
		if !p.IsAdmin {
			return nil, nil, kbErr("FORBIDDEN", "An administrator is required.")
		}
		name := stringArg(a, "name")
		if strings.TrimSpace(name) == "" || len([]rune(name)) > 200 {
			return nil, nil, badArg("name must contain 1–200 characters.")
		}
		target = "service_" + digest([]byte(p.IdentityID + "\x00" + stringArg(a, "request_id")))[:32]
		id := Identity{ID: target, Name: name, Type: "service"}
		if existing, ok := ids[target]; ok {
			id = existing
		} else {
			ids[target] = id
		}
		changes = append(changes, jsonChange(filepath.Join(s.private, "identities.json"), ids))
		result["identity_id"] = target
		result["identity"] = id
	case "disable_service_account":
		if !p.IsAdmin {
			return nil, nil, kbErr("FORBIDDEN", "An administrator is required.")
		}
		id, ok := ids[target]
		if !ok || id.Type != "service" {
			return nil, nil, kbErr("NOT_FOUND", "Resource not found.")
		}
		id.Disabled = true
		ids[target] = id
		changes = append(changes, jsonChange(filepath.Join(s.private, "identities.json"), ids))
	default:
		return nil, nil, badArg("Unknown access action.")
	}
	changes = append([]fileChange{{Access: &security}}, changes...)

	return result, changes, nil
}
func entryVersion(e Entry) string {
	return "v_" + digest([]byte(fmt.Sprintf("%s:%d:%s", e.ID, e.Sequence, e.Fingerprint)))[:40]
}
func childPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return path.Join(parent, name)
}

func (s *Service) registerAdministrator(ctx context.Context, id string) error {
	unlock, e := s.lock(ctx, false)
	if e != nil {
		return e
	}
	if e = s.recover(); e != nil {
		unlock()
		return e
	}
	ids, e := s.identities()
	unlock()
	if e != nil {
		return e
	}
	if _, exists := ids[id]; exists {
		return nil
	}
	release, e := s.publicationLock()
	if e != nil {
		return e
	}
	defer release()
	unlock, e = s.lock(ctx, false)
	if e != nil {
		return e
	}
	defer unlock()
	if e = s.recover(); e != nil {
		return e
	}
	ids, e = s.identities()
	if e != nil {
		return e
	}
	if _, exists := ids[id]; exists {
		return nil
	}
	ids[id] = Identity{ID: id, Name: id, Type: "user"}
	return s.transact([]fileChange{{Access: &accessChange{OnlyGeneration: true, SecurityGeneration: s.nextSecurityGeneration()}}, jsonChange(filepath.Join(s.private, "identities.json"), ids)})
}

func (s *Service) aclToken(folderID string, sequence int) string {
	return "acl_" + digest([]byte(fmt.Sprintf("%s:%s:%d", s.cfg.OrganizationID, folderID, sequence)))[:32]
}
func (s *Service) aclVersion(folderID string) string {
	sequence := 1
	if e := s.db.QueryRow(`SELECT value FROM security WHERE key=?`, "acl:"+folderID).Scan(&sequence); e != nil {
		sequence = 1
	}
	return s.aclToken(folderID, sequence)
}

func (s *Service) accessDiscovery(p Principal, a map[string]any) (any, error) {
	_, explicitPath := a["folder_path"]
	_, explicitID := a["folder_id"]
	var out map[string]any
	if explicitPath || explicitID {
		v, e := s.access(p, a)
		if e != nil {
			return nil, e
		}
		out = asMap(v)
	} else if s.effective(p, "") >= roleReader {
		v, e := s.access(p, map[string]any{"folder_path": ""})
		if e != nil {
			return nil, e
		}
		out = asMap(v)
	} else {
		out = map[string]any{"folder_path": "", "effective_role": "None", "grants": []any{}, "identities": []Identity{}}
	}
	args := map[string]any{"folder_path": stringArg(out, "folder_path"), "depth": 1024}
	for _, k := range []string{"cursor", "limit"} {
		if v, ok := a[k]; ok {
			args[k] = v
		}
	}
	v, e := s.list(p, "list_knowledgebase_folders", args)
	if e != nil {
		return nil, e
	}
	m := asMap(v)
	out["folders"] = m["items"]
	out["next_cursor"] = m["next_cursor"]
	if !explicitPath && !explicitID && s.effective(p, "") < roleOwner {
		rs, e := s.registries()
		if e != nil {
			return nil, e
		}
		owner := false
		for _, r := range rs {
			if s.effective(p, r.Path) >= roleOwner {
				owner = true
				break
			}
		}
		if owner {
			ids, e := s.identities()
			if e != nil {
				return nil, e
			}
			identities := []Identity{}
			for _, id := range ids {
				identities = append(identities, id)
			}
			sort.Slice(identities, func(i, j int) bool { return identities[i].Name < identities[j].Name })
			out["identities"] = identities
		}
	}
	return out, nil
}
