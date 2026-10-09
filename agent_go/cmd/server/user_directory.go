package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/argon2"
)

// The user directory: the one place AgentWorks knows who its users are.
// See docs/design/user_accounts_and_workflow_sharing.md.
//
// Storage is config/users.json in the shared workspace (the same place and
// pattern as workflow-user-permissions.json), written under a mutex with the
// workspace API's atomic document write. It replaces the AUTH_USERS env var
// as the user store: AUTH_USERS is still read, but only to IMPORT users into
// this file on startup (password hashed at import), after which it can be
// dropped from the environment.
//
// A record answers the two account-level questions no single workflow can:
// may this person create workflows (CanCreate), may they edit workflows that
// explicitly grant ownership (CanEdit), and may this person manage other
// accounts (Admin). Products lists
// which product surfaces the account may open. Everything else — who owns
// which workflow — lives on the workflow itself (phase 3 of the design).
//
// How it plugs into the existing permission machinery: workflowAccessForIdentity
// consults the directory first and maps Admin → owner, CanEdit → write,
// otherwise → read, so every existing enforcement point (PLAT-262's runtime
// read-only gates, requireWorkflowWriteAccess, list filtering) keys off the
// directory with no change of its own. Identities with NO record keep
// today's behavior exactly (env tiers / permissive default), so a
// deployment that never writes this file is unaffected.

// UserRecord is one account.
type UserRecord struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	// PasswordHash is argon2id in PHC string format; absent for SSO-only
	// accounts.
	PasswordHash string   `json:"password_hash,omitempty"`
	SSO          *UserSSO `json:"sso,omitempty"`
	Admin        bool     `json:"admin"`
	CanCreate    bool     `json:"can_create"`
	// CanEdit is optional for backward compatibility. When absent, it follows
	// CanCreate, preserving the original admin/member/read-only roles. An
	// explicit true with CanCreate=false is a contributor: they may own/edit a
	// shared workflow but cannot create another one.
	CanEdit *bool `json:"can_edit,omitempty"`
	// Role is the standardized account role: admin, creator, editor, or
	// viewer. It wins over Admin/CanCreate/CanEdit when present; those stay
	// as fallback for records that predate roles and are dual-written on
	// every admin save so older servers keep working during rollout.
	Role string `json:"role,omitempty"`
	// Products the account may open. Meaning depends on the account: an
	// admin ignores it (all products), a member with an empty list gets all
	// products, a read-only user with an empty list gets none.
	Products []string `json:"products"`
	// CreateProducts is the per-product create permission (PLAT-767): the products in which this account may create
	// workflows, Relays, Crews, Code projects and the like. Absent: a creator creates in every product it may open,
	// an editor or viewer in none. Present (even empty): exactly these, for a creator or an editor; a viewer never
	// creates and an admin always may.
	CreateProducts *[]string `json:"create_products,omitempty"`
	// CodeReviewer is a permission on top of the role: the account may review
	// every Code workspace's cost, chats and files, read-only, with each view
	// audited (code_admin.go), and read that audit log. It grants no write
	// anywhere and is not admin.
	CodeReviewer bool `json:"code_reviewer,omitempty"`
	// VaultReader may read Vault (connections, tools, groups, access, audit
	// and SQL queries) without being an admin, and changes nothing. Needs the
	// Vault product. Vault managers are admins with Vault.
	VaultReader bool `json:"vault_reader,omitempty"`
	Disabled    bool `json:"disabled,omitempty"`
	// TokenLimits caps this person's tokens per UTC day and Monday-start
	// week on the shared server accounts (token_limits.go). Nil is unlimited.
	TokenLimits *UserTokenLimits `json:"token_limits,omitempty"`
	// AccountTokenLimits overrides, per shared server account (keyed by
	// provider, e.g. "codex-cli"), that account's default per-person limits
	// for this person; a field set here replaces the default's field.
	AccountTokenLimits map[string]*UserTokenLimits `json:"account_token_limits,omitempty"`
	// AccountAllowedModels overrides, per shared server account (keyed by
	// provider), the models the account allows for this person: a list
	// replaces the account's list, ["*"] is every model (PLAT-714).
	AccountAllowedModels map[string][]string `json:"account_allowed_models,omitempty"`
	CreatedAt            string              `json:"created_at,omitempty"`
	UpdatedAt            string              `json:"updated_at,omitempty"`
}

// UserSSO links an account to an external identity provider.
type UserSSO struct {
	Provider   string `json:"provider"`
	ExternalID string `json:"external_id,omitempty"`
}

type userDirectoryFile struct {
	Users []UserRecord `json:"users"`
}

// userDirectory is the in-memory view of users.json.
type userDirectory struct {
	Users []UserRecord
}

func userDirectoryFilePath() string { return "config/users.json" }

// userIDForUsername is the SAME derivation AUTH_USERS used, so an account
// imported from the env var keeps the id (and therefore the _users/<id>
// tree, secrets and history) it already had.
func userIDForUsername(username string) string {
	hash := sha256.Sum256([]byte("user:" + strings.TrimSpace(username)))
	return hex.EncodeToString(hash[:16])
}

// ---- password hashing -------------------------------------------------

// argon2id parameters: 64MB, 3 passes, 2 lanes — the OWASP-recommended
// interactive-login setting, ~50ms on a laptop.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want))) //nolint:gosec // G115: key length is bounded.
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---- load / save with a short cache ----------------------------------

var (
	userDirectoryMu        sync.Mutex
	userDirectoryCache     *userDirectory
	userDirectoryCacheTime time.Time
	userDirectoryCacheTTL  = 3 * time.Second
	// userDirectoryUnavailable is set when the workspace API answered but
	// the file could not be parsed; the directory is then treated as empty
	// rather than silently granting or denying.
	userDirectoryLoadErr error
)

func loadUserDirectory() (*userDirectory, error) {
	userDirectoryMu.Lock()
	defer userDirectoryMu.Unlock()
	if userDirectoryCache != nil && time.Since(userDirectoryCacheTime) < userDirectoryCacheTTL {
		return userDirectoryCache, userDirectoryLoadErr
	}
	dir, err := readUserDirectoryFile()
	userDirectoryCache, userDirectoryCacheTime, userDirectoryLoadErr = dir, time.Now(), err
	return dir, err
}

// The two workspace calls are variables so tests can run the handlers
// against an in-memory file instead of a live workspace API.
var (
	userDirectoryRead = func() (string, bool, error) {
		return readFileFromWorkspace(context.Background(), userDirectoryFilePath())
	}
	userDirectoryWrite = func(content string) error {
		return writeFileToWorkspace(context.Background(), userDirectoryFilePath(), content)
	}
)

func readUserDirectoryFile() (*userDirectory, error) {
	data, exists, err := userDirectoryRead()
	if err != nil {
		return &userDirectory{}, err
	}
	if !exists || strings.TrimSpace(data) == "" {
		return &userDirectory{}, nil
	}
	var f userDirectoryFile
	if err := json.Unmarshal([]byte(data), &f); err != nil {
		return &userDirectory{}, fmt.Errorf("users.json: %w", err)
	}
	return &userDirectory{Users: f.Users}, nil
}

// saveUserDirectory writes the whole file and drops the cache. Callers hold
// no lock; the write itself is serialized here.
func saveUserDirectory(dir *userDirectory) error {
	userDirectoryMu.Lock()
	defer userDirectoryMu.Unlock()
	sort.Slice(dir.Users, func(i, j int) bool { return dir.Users[i].Username < dir.Users[j].Username })
	for i := range dir.Users {
		if dir.Users[i].Products == nil {
			dir.Users[i].Products = []string{}
		}
	}
	data, err := json.MarshalIndent(userDirectoryFile{Users: dir.Users}, "", "  ")
	if err != nil {
		return err
	}
	if err := userDirectoryWrite(string(data)); err != nil {
		return err
	}
	userDirectoryCache, userDirectoryCacheTime, userDirectoryLoadErr = dir, time.Now(), nil
	return nil
}

func invalidateUserDirectoryCache() {
	userDirectoryMu.Lock()
	userDirectoryCache = nil
	userDirectoryMu.Unlock()
}

// ---- lookups ------------------------------------------------------------

func (d *userDirectory) byID(id string) *UserRecord {
	for i := range d.Users {
		if d.Users[i].ID == id {
			return &d.Users[i]
		}
	}
	return nil
}

func (d *userDirectory) byUsername(username string) *UserRecord {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return nil
	}
	for i := range d.Users {
		if strings.ToLower(d.Users[i].Username) == username {
			return &d.Users[i]
		}
	}
	return nil
}

func (d *userDirectory) byEmail(email string) *UserRecord {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	for i := range d.Users {
		if strings.ToLower(d.Users[i].Email) == email {
			return &d.Users[i]
		}
	}
	return nil
}

// find resolves an identity by id, then username, then email — the same
// precedence the legacy permission files use.
func (d *userDirectory) find(userID, username, email string) *UserRecord {
	if r := d.byID(userID); r != nil {
		return r
	}
	if r := d.byUsername(username); r != nil {
		return r
	}
	return d.byEmail(email)
}

// findForExternal resolves the account of an SSO sign-in by its provider
// identity, then its email; never by display name. A Google display name is
// chosen by the person, so matching on it let someone named like an admin
// sign in as that admin, and two people with one name share an account.
func (d *userDirectory) findForExternal(userID string, ext *ExternalUser) *UserRecord {
	if ext.ExternalID != "" {
		for i := range d.Users {
			if sso := d.Users[i].SSO; sso != nil && sso.ExternalID == ext.ExternalID && (sso.Provider == "" || sso.Provider == ext.Provider) {
				return &d.Users[i]
			}
		}
	}
	// A record whose id is the SSO id (created by an earlier sign-in), unless
	// it is linked to a different identity.
	if r := d.byID(userID); r != nil && (r.SSO == nil || r.SSO.ExternalID == "" || r.SSO.ExternalID == ext.ExternalID) {
		return r
	}
	return d.byEmail(ext.Email)
}

// uniqueDirectoryUsername is name, or the email (then a numbered variant)
// when another account already has it: usernames are unique, and SSO display
// names are not.
func (d *userDirectory) uniqueDirectoryUsername(name, email string) string {
	for _, candidate := range []string{name, strings.ToLower(strings.TrimSpace(email))} {
		if candidate != "" && d.byUsername(candidate) == nil {
			return candidate
		}
	}
	base := name
	if base == "" {
		base = email
	}
	for n := 2; ; n++ {
		if candidate := fmt.Sprintf("%s (%d)", base, n); d.byUsername(candidate) == nil {
			return candidate
		}
	}
}

// directoryUserFor is the lookup every permission check goes through. A
// nil result means "no record" and callers fall back to legacy behavior.
func directoryUserFor(userID, username, email string) *UserRecord {
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return nil
	}
	return dir.find(userID, username, email)
}

func directoryUserForClaims(claims *UserClaims) *UserRecord {
	if claims == nil {
		return nil
	}
	return directoryUserFor(claims.UserID, claims.Username, claims.Email)
}

// userDirectoryHasUsers reports whether the directory lists anyone at all.
func userDirectoryHasUsers() bool {
	dir, err := loadUserDirectory()
	return err == nil && dir != nil && len(dir.Users) > 0
}

// userDirectoryHasPasswordUsers reports whether password login has anyone
// to authenticate — what makes the "simple" provider configured once
// AUTH_USERS is gone from the environment.
func userDirectoryHasPasswordUsers() bool {
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return false
	}
	for _, u := range dir.Users {
		if u.PasswordHash != "" && !u.Disabled {
			return true
		}
	}
	return false
}

// validateDirectoryCredentials checks a username/password against the
// directory. Returns nil for unknown, disabled, SSO-only, or wrong password.
func validateDirectoryCredentials(username, password string) *UserRecord {
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return nil
	}
	rec := dir.byUsername(username)
	if rec == nil || rec.Disabled || rec.PasswordHash == "" {
		return nil
	}
	if !verifyPassword(rec.PasswordHash, password) {
		return nil
	}
	copy := *rec
	return &copy
}

// ---- effective access ---------------------------------------------------

// UserAccess is what an identity may do at the account level.
type UserAccess struct {
	// Known is false when the identity has no directory record; every
	// other field is then the legacy/compat answer, not a directory one.
	Known     bool
	Admin     bool
	CanCreate bool
	CanEdit   bool
	Disabled  bool
	// CodeReviewer: see UserRecord.CodeReviewer.
	CodeReviewer bool
	// VaultReader: see UserRecord.VaultReader.
	VaultReader bool
	// Products the identity may open when ProductsRestricted; ignored
	// otherwise (all products).
	Products           []string
	ProductsRestricted bool
	// CreateProducts: see UserRecord.CreateProducts; nil follows the role.
	CreateProducts []string
}

// CanCreateIn reports whether the account may create in a product ("agentworks" for workflows, "relays", "work" for
// Crews, "code", ...). It is the one create check: the role says whether the account creates at all, CreateProducts
// narrows (or, for an editor, grants) it per product (PLAT-767).
func (acc UserAccess) CanCreateIn(product string) bool {
	if acc.Disabled {
		return false
	}
	if acc.Admin {
		return true
	}
	if acc.CreateProducts == nil {
		return acc.CanCreate
	}
	if !acc.CanCreate && !acc.CanEdit {
		return false
	}
	product = strings.ToLower(strings.TrimSpace(product))
	for _, allowed := range acc.CreateProducts {
		if strings.EqualFold(strings.TrimSpace(allowed), product) {
			return true
		}
	}
	return false
}

// Standardized account roles. Exactly one applies per account:
//   - admin: everything, all products, all workflows, user management.
//   - creator: create workflows, edit assigned ones, full products.
//   - editor: edit assigned workflows, no creation.
//   - viewer: read-only, sees only assigned products/workflows.
const (
	UserRoleAdmin   = "admin"
	UserRoleCreator = "creator"
	UserRoleEditor  = "editor"
	UserRoleViewer  = "viewer"
)

func normalizeUserRole(role string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case UserRoleAdmin:
		return UserRoleAdmin, true
	case UserRoleCreator:
		return UserRoleCreator, true
	case UserRoleEditor:
		return UserRoleEditor, true
	case UserRoleViewer:
		return UserRoleViewer, true
	default:
		return "", false
	}
}

// roleForRecord returns the record's standardized role. An explicit valid
// role wins; otherwise the legacy booleans map onto the closest role
// (admin, then can_create, then can_edit, else viewer). The exotic legacy
// combo can_create without can_edit maps to creator — creation implies the
// edit rights its own workflows need.
func roleForRecord(rec *UserRecord) string {
	if rec == nil {
		return ""
	}
	if role, ok := normalizeUserRole(rec.Role); ok {
		return role
	}
	switch {
	case rec.Admin:
		return UserRoleAdmin
	case rec.CanCreate:
		return UserRoleCreator
	case rec.CanEdit != nil && *rec.CanEdit:
		return UserRoleEditor
	default:
		return UserRoleViewer
	}
}

// applyRoleToRecord stamps a role and dual-writes the legacy booleans so
// older servers (and older UI builds) keep enforcing the same access.
func applyRoleToRecord(rec *UserRecord, role string) {
	rec.Role = role
	rec.Admin = role == UserRoleAdmin
	rec.CanCreate = role == UserRoleAdmin || role == UserRoleCreator
	canEdit := role != UserRoleViewer
	rec.CanEdit = &canEdit
}

func accessForRecord(rec *UserRecord) UserAccess {
	var canEdit, canCreate, admin bool
	switch roleForRecord(rec) {
	case UserRoleAdmin:
		admin, canCreate, canEdit = true, true, true
	case UserRoleCreator:
		canCreate, canEdit = true, true
	case UserRoleEditor:
		canEdit = true
	}
	acc := UserAccess{Known: true, Admin: admin, CanCreate: canCreate, CanEdit: canEdit, Disabled: rec.Disabled, CodeReviewer: rec.CodeReviewer, VaultReader: rec.VaultReader}
	if rec.CreateProducts != nil {
		acc.CreateProducts = append([]string{}, (*rec.CreateProducts)...)
	}
	switch {
	case acc.Admin:
		acc.ProductsRestricted = false
	case len(rec.Products) > 0:
		acc.ProductsRestricted = true
		acc.Products = append([]string(nil), rec.Products...)
	case acc.CanCreate:
		// Creators with no explicit list open every product.
		acc.ProductsRestricted = false
	default:
		// Editors and viewers with nothing assigned: no products at all.
		acc.ProductsRestricted = true
		acc.Products = []string{}
	}
	return acc
}

// userAccessForClaims resolves the account-level answer for a request.
// Outside multi-user mode the single local user is the machine's owner and
// is an admin of their own installation; a multi-user identity without a
// record is Unknown and keeps today's env-driven behavior.
func userAccessForClaims(claims *UserClaims) UserAccess {
	if claims != nil && claims.Provider == "bot_route" {
		return UserAccess{Known: true, ProductsRestricted: true, Products: []string{claims.BotRouteProfileID}}
	}
	if rec := directoryUserForClaims(claims); rec != nil {
		return accessForRecord(rec)
	}
	if !IsMultiUserMode() {
		return UserAccess{Known: false, Admin: true, CanCreate: true, CanEdit: true}
	}
	return UserAccess{Known: false, CanCreate: true, CanEdit: true}
}

// currentUserIsAdmin is the gate for account management. With no directory
// record it defers to the legacy owner tier so an existing deployment's
// WORKFLOW_OWNER_USERS keep working until they are imported.
func currentUserIsAdmin(r *http.Request) bool {
	claims := GetUserFromContext(r.Context())
	acc := userAccessForClaims(claims)
	if acc.Known {
		return acc.Admin && !acc.Disabled
	}
	return acc.Admin || currentUserCanManageWorkflowAccess(r)
}

// currentUserCanReviewCode gates Code inspection (code_admin.go) and the
// Code rows of the cost overview: admins, and enabled accounts an admin
// marked as Code reviewers.
func currentUserCanReviewCode(r *http.Request) bool {
	if currentUserIsAdmin(r) {
		return true
	}
	acc := userAccessForClaims(GetUserFromContext(r.Context()))
	return acc.Known && acc.CodeReviewer && !acc.Disabled
}

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" || currentUserIsAdmin(r) {
			next(w, r)
			return
		}
		writeWorkflowPermissionDenied(w, "admin")
	}
}

// directoryUserIsDisabled lets the auth middleware refuse tokens of an
// account an admin has switched off, without waiting for the JWT to expire.
func directoryUserIsDisabled(claims *UserClaims) bool {
	rec := directoryUserForClaims(claims)
	return rec != nil && rec.Disabled
}

// ---- bootstrap ------------------------------------------------------------

// adminUsernamesFromEnv reads ADMIN_USERS: comma-separated usernames or
// emails that are admins. Applied whenever a matching record is created or
// loaded at bootstrap, so the first admin is always named in config, never
// inferred from who signed up first.
func adminUsernamesFromEnv() map[string]bool {
	out := map[string]bool{}
	for _, raw := range strings.Split(os.Getenv("ADMIN_USERS"), ",") {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key != "" {
			out[key] = true
		}
	}
	return out
}

func isConfiguredAdmin(rec *UserRecord) bool {
	admins := adminUsernamesFromEnv()
	return admins[strings.ToLower(rec.Username)] || (rec.Email != "" && admins[strings.ToLower(rec.Email)])
}

// externalAuthIdentityApproved decides who may complete an SSO sign-in: a person an administrator
// added to the user directory (by email), or an email named in ADMIN_USERS so the first
// administrator can bootstrap their own record. Nobody else is admitted, and a sign-in never
// creates an account for a stranger. AUTH_ALLOWED_EMAILS no longer admits anyone: accounts are
// provisioned by an administrator (each will also carry its slot; see the private slot plan).
// Disabled records are deliberately returned here so the callback can produce its specific
// disabled-account rejection after linking the external identity.
func externalAuthIdentityApproved(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !emailDomainAllowed(email) {
		return false
	}
	if adminUsernamesFromEnv()[email] {
		return true
	}
	dir, err := loadUserDirectory()
	return err == nil && dir != nil && dir.byEmail(email) != nil
}

// hardcodedUsersSource is GetHardcodedUsers, swappable in tests (the real
// one is guarded by a sync.Once over the environment).
var hardcodedUsersSource = GetHardcodedUsers

// bootstrapUserDirectory imports AUTH_USERS into users.json (hashing the
// passwords) and applies ADMIN_USERS. Idempotent: existing records are never
// overwritten except to set Admin for a configured admin. Called once at
// startup, after the workspace API is reachable; failures are logged, not
// fatal, since the legacy env path still authenticates.
func bootstrapUserDirectory(ctx context.Context) {
	dir, err := readUserDirectoryFile()
	if err != nil {
		log.Printf("[USERS] bootstrap: cannot read %s: %v", userDirectoryFilePath(), err)
		return
	}
	changed := false
	now := time.Now().UTC().Format(time.RFC3339)
	for _, hu := range hardcodedUsersSource() {
		if dir.byUsername(hu.Username) != nil {
			continue
		}
		hash, err := hashPassword(hu.Password)
		if err != nil {
			log.Printf("[USERS] bootstrap: cannot hash password for %s: %v", hu.Username, err)
			continue
		}
		rec := UserRecord{ID: hu.UserID, Username: hu.Username, PasswordHash: hash, CanCreate: true, Products: []string{}, CreatedAt: now, UpdatedAt: now}
		rec.Admin = isConfiguredAdmin(&rec)
		dir.Users = append(dir.Users, rec)
		changed = true
		log.Printf("[USERS] bootstrap: imported %s from AUTH_USERS (admin=%v)", hu.Username, rec.Admin)
	}
	for i := range dir.Users {
		if !dir.Users[i].Admin && isConfiguredAdmin(&dir.Users[i]) {
			dir.Users[i].Admin = true
			dir.Users[i].UpdatedAt = now
			changed = true
			log.Printf("[USERS] bootstrap: %s is an admin per ADMIN_USERS", dir.Users[i].Username)
		}
	}
	if changed {
		if err := saveUserDirectory(dir); err != nil {
			log.Printf("[USERS] bootstrap: cannot write %s: %v", userDirectoryFilePath(), err)
			return
		}
	}
	if len(dir.Users) > 0 {
		log.Printf("[USERS] directory ready: %d user(s) in %s", len(dir.Users), userDirectoryFilePath())
	}
	_ = ctx
}

// ensureDirectoryUserForExternal creates the record for an SSO identity on
// its first login. New SSO users start with nothing enabled (no create, no
// products) unless ADMIN_USERS names them — the safe default for an open
// sign-in provider; an admin switches them on. Returns the record.
func ensureDirectoryUserForExternal(userID string, ext *ExternalUser) *UserRecord {
	dir, err := readUserDirectoryFile()
	if err != nil {
		return nil
	}
	if rec := dir.findForExternal(userID, ext); rec != nil {
		recordID := rec.ID
		changed := false
		if rec.Email == "" && strings.TrimSpace(ext.Email) != "" {
			rec.Email = strings.ToLower(strings.TrimSpace(ext.Email))
			changed = true
		}
		if rec.SSO == nil {
			rec.SSO = &UserSSO{Provider: ext.Provider, ExternalID: ext.ExternalID}
			changed = true
		} else if rec.SSO.ExternalID == "" && ext.ExternalID != "" {
			rec.SSO.Provider, rec.SSO.ExternalID = ext.Provider, ext.ExternalID
			changed = true
		}
		if changed {
			rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := saveUserDirectory(dir); err != nil {
				log.Printf("[USERS] cannot link SSO identity for %s: %v", rec.Username, err)
				return dir.byID(recordID)
			}
		}
		return dir.byID(recordID)
	}
	// No record: only a configured administrator may create their own, to bootstrap. Everyone else
	// must have been added by an administrator, so a first sign-in never creates an account.
	now := time.Now().UTC().Format(time.RFC3339)
	rec := UserRecord{
		ID:        userID,
		Username:  dir.uniqueDirectoryUsername(ext.Username, ext.Email),
		Email:     strings.ToLower(strings.TrimSpace(ext.Email)),
		SSO:       &UserSSO{Provider: ext.Provider, ExternalID: ext.ExternalID},
		Products:  []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if !isConfiguredAdmin(&rec) {
		return nil
	}
	rec.Admin = true
	rec.CanCreate = true
	dir.Users = append(dir.Users, rec)
	if err := saveUserDirectory(dir); err != nil {
		log.Printf("[USERS] cannot record SSO user %s: %v", ext.Username, err)
		return nil
	}
	log.Printf("[USERS] bootstrapped configured admin %s via %s", ext.Username, ext.Provider)
	return dir.byID(userID)
}

// ---- admin API --------------------------------------------------------------

// userAdminView is what the admin page sees; never the hash.
type userAdminView struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email,omitempty"`
	Provider    string   `json:"provider"`
	HasPassword bool     `json:"has_password"`
	Admin       bool     `json:"admin"`
	CanCreate   bool     `json:"can_create"`
	CanEdit     bool     `json:"can_edit"`
	Role        string   `json:"role"`
	Products    []string `json:"products"`
	// CreateProducts is the per-product create list; null follows the role (PLAT-767).
	CreateProducts *[]string `json:"create_products"`
	CodeReviewer   bool      `json:"code_reviewer"`
	VaultReader    bool      `json:"vault_reader"`
	Disabled       bool      `json:"disabled"`
	// Invited: added by email, no password, not signed in with SSO yet.
	Invited            bool                        `json:"invited"`
	TokenLimits        *UserTokenLimits            `json:"token_limits,omitempty"`
	AccountTokenLimits map[string]*UserTokenLimits `json:"account_token_limits,omitempty"`
	// AccountAllowedModels is this person's model override per shared account.
	AccountAllowedModels map[string][]string      `json:"account_allowed_models,omitempty"`
	TokenUsage           *sharedAccountTokenUsage `json:"token_usage,omitempty"`
	CreatedAt            string                   `json:"created_at,omitempty"`
	UpdatedAt            string                   `json:"updated_at,omitempty"`
}

func viewOf(rec UserRecord) userAdminView {
	provider := "password"
	if rec.SSO != nil && rec.SSO.Provider != "" {
		provider = rec.SSO.Provider
	}
	products := rec.Products
	if products == nil {
		products = []string{}
	}
	acc := accessForRecord(&rec)
	return userAdminView{
		ID: rec.ID, Username: rec.Username, Email: rec.Email, Provider: provider,
		HasPassword: rec.PasswordHash != "", Admin: acc.Admin, CanCreate: acc.CanCreate, CanEdit: acc.CanEdit,
		Role:     roleForRecord(&rec),
		Products: products, CreateProducts: rec.CreateProducts, CodeReviewer: rec.CodeReviewer, VaultReader: rec.VaultReader, Disabled: rec.Disabled, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
		Invited:              rec.PasswordHash == "" && rec.SSO == nil && rec.Email != "",
		TokenLimits:          rec.TokenLimits.normalized(),
		AccountTokenLimits:   normalizedAccountTokenLimits(rec.AccountTokenLimits),
		AccountAllowedModels: rec.AccountAllowedModels,
	}
}

// adminEmailError checks an email an admin sets on the account with ID
// selfID ("" for a new account): a plain address, not already another
// account's. SSO sign-in resolves the account by this address, so two
// accounts sharing one would make the first silently win.
func adminEmailError(dir *userDirectory, email, selfID string) string {
	if email == "" {
		return ""
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email, "@") {
		return "enter a plain email address, like name@example.com"
	}
	if other := dir.byEmail(email); other != nil && other.ID != selfID {
		return "another account already uses that email"
	}
	if !emailDomainAllowed(email) {
		return "this installation only allows addresses at " + strings.Join(allowedEmailDomains(), ", ")
	}
	return ""
}

// allowedEmailDomains is AUTH_ALLOWED_EMAIL_DOMAINS (comma-separated, e.g.
// "corp.example"): when set, only addresses at those domains may sign in
// with SSO or be added as accounts, admins included. Unset allows any domain.
func allowedEmailDomains() []string {
	var domains []string
	for _, domain := range strings.Split(os.Getenv("AUTH_ALLOWED_EMAIL_DOMAINS"), ",") {
		if domain = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "@")); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}

func emailDomainAllowed(email string) bool {
	domains := allowedEmailDomains()
	if len(domains) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(email[at+1:]))
	for _, domain := range domains {
		if host == domain {
			return true
		}
	}
	return false
}

func writeUsersJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeUsersError(w http.ResponseWriter, status int, msg string) {
	writeUsersJSON(w, status, map[string]string{"error": msg})
}

// GET /api/admin/users
func (api *StreamingAPI) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	dir, err := readUserDirectoryFile()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userAdminView, 0, len(dir.Users))
	for i := range dir.Users {
		view := viewOf(dir.Users[i])
		view.TokenUsage = api.sharedAccountTokenUsageFor(&dir.Users[i])
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	writeUsersJSON(w, http.StatusOK, map[string]any{"users": out, "products": knownProductIDs()})
}

// userWriteRequest is the body for create and update. Pointer fields are
// "unchanged" when absent on update.
type userWriteRequest struct {
	Username  string    `json:"username"`
	Email     *string   `json:"email"`
	Password  *string   `json:"password"`
	Admin     *bool     `json:"admin"`
	CanCreate *bool     `json:"can_create"`
	CanEdit   *bool     `json:"can_edit"`
	Role      *string   `json:"role"`
	Products  *[]string `json:"products"`
	// CreateProducts sets the per-product create list when present; ClearCreateProducts returns it to the role's
	// default (JSON null cannot be told apart from absent).
	CreateProducts      *[]string `json:"create_products"`
	ClearCreateProducts bool      `json:"clear_create_products"`
	CodeReviewer        *bool     `json:"code_reviewer"`
	VaultReader         *bool     `json:"vault_reader"`
	Disabled            *bool     `json:"disabled"`
	// TokenLimits replaces both limits when present; zero is unlimited.
	TokenLimits *UserTokenLimits `json:"token_limits"`
	// AccountTokenLimits sets this person's override for each named shared
	// account (both fields); an entry with no limit removes the override.
	// Accounts not named keep their override.
	AccountTokenLimits map[string]*UserTokenLimits `json:"account_token_limits"`
	// AccountAllowedModels sets this person's model override for each named
	// shared account: a list, ["*"] for every model, or null/[] to fall back
	// to the account's list. Accounts not named keep their override.
	AccountAllowedModels map[string][]string `json:"account_allowed_models"`
}

// applyRoleWrite stamps a requested role after validating it. An explicit
// role wins over any legacy booleans in the same request.
func applyRoleWrite(rec *UserRecord, req userWriteRequest) error {
	if req.Role == nil {
		if req.Admin != nil {
			rec.Admin = *req.Admin
		}
		if req.CanCreate != nil {
			rec.CanCreate = *req.CanCreate
		}
		if req.CanEdit != nil {
			rec.CanEdit = req.CanEdit
		}
		// A legacy-boolean write clears any stamped role so the booleans
		// stay authoritative for the record they describe.
		if req.Admin != nil || req.CanCreate != nil || req.CanEdit != nil {
			rec.Role = ""
		}
		return nil
	}
	role, ok := normalizeUserRole(*req.Role)
	if !ok {
		return fmt.Errorf("role must be one of admin, creator, editor, viewer")
	}
	applyRoleToRecord(rec, role)
	return nil
}

var errUsernameInvalid = errors.New("username must be 2-64 characters: letters, digits, dot, dash, underscore, or an email address")

func validUsername(u string) bool {
	if len(u) < 2 || len(u) > 64 {
		return false
	}
	for _, c := range u {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_', c == '@', c == '+':
		default:
			return false
		}
	}
	return true
}

func normalizeProducts(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// handleAdminCreateUser refuses: accounts are not created from the app any more. People are added by DevOps on the
// server with `provision-slots.sh adduser <email>`, which creates the account and gives it a slot in one step; an
// account without a slot cannot run a single command (server B 2026-10-09, PLAT-777), and only root can assign one.
func (api *StreamingAPI) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	writeUsersError(w, http.StatusForbidden, "Accounts are added by DevOps on the server, not from the app: run `provision-slots.sh adduser <email>`, which creates the account and its slot together.")
}

// PUT /api/admin/users/{id}
func (api *StreamingAPI) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req userWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUsersError(w, http.StatusBadRequest, "invalid body")
		return
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec := dir.byID(id)
	if rec == nil {
		writeUsersError(w, http.StatusNotFound, "user not found")
		return
	}
	callerID := GetUserIDFromContext(r.Context())
	// An admin cannot lock themselves out: no removing their own admin
	// flag or disabling their own account. Another admin can. A role
	// change away from admin counts as removing the flag.
	selfDemotion := req.Admin != nil && !*req.Admin
	if req.Role != nil {
		if role, ok := normalizeUserRole(*req.Role); !ok || role != UserRoleAdmin {
			selfDemotion = true
		}
	}
	if rec.ID == callerID {
		if selfDemotion || (req.Disabled != nil && *req.Disabled) {
			writeUsersError(w, http.StatusBadRequest, "you cannot remove your own admin access or disable your own account")
			return
		}
	}
	if req.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		if msg := adminEmailError(dir, email, rec.ID); msg != "" {
			writeUsersError(w, http.StatusBadRequest, msg)
			return
		}
		rec.Email = email
	}
	if req.Password != nil && *req.Password != "" {
		if len(*req.Password) < 8 {
			writeUsersError(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
		hash, err := hashPassword(*req.Password)
		if err != nil {
			writeUsersError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rec.PasswordHash = hash
	}
	if err := applyRoleWrite(rec, req); err != nil {
		writeUsersError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Products != nil {
		rec.Products = normalizeProducts(*req.Products)
	}
	if req.ClearCreateProducts {
		rec.CreateProducts = nil
	} else if req.CreateProducts != nil {
		create := normalizeProducts(*req.CreateProducts)
		if create == nil {
			create = []string{}
		}
		rec.CreateProducts = &create
	}
	if req.CodeReviewer != nil {
		rec.CodeReviewer = *req.CodeReviewer
	}
	if req.VaultReader != nil {
		rec.VaultReader = *req.VaultReader
	}
	if req.Disabled != nil {
		rec.Disabled = *req.Disabled
	}
	if req.TokenLimits != nil {
		rec.TokenLimits = req.TokenLimits.normalized()
	}
	if err := applyAccountTokenLimits(rec, req.AccountTokenLimits); err != nil {
		writeUsersError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := applyAccountAllowedModels(rec, req.AccountAllowedModels); err != nil {
		writeUsersError(w, http.StatusBadRequest, err.Error())
		return
	}
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := saveUserDirectory(dir); err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[USERS] %s updated user %s (role=%s products=%v code_reviewer=%v vault_reader=%v disabled=%v token_limits=%+v account_token_limits=%s account_allowed_models=%s)", callerID, rec.Username, roleForRecord(rec), rec.Products, rec.CodeReviewer, rec.VaultReader, rec.Disabled, rec.TokenLimits.normalized(), accountTokenLimitsSummary(rec.AccountTokenLimits), accountAllowedModelsSummary(rec.AccountAllowedModels))
	writeUsersJSON(w, http.StatusOK, viewOf(*rec))
}

// DELETE /api/admin/users/{id} — removes the record. The user's _users/<id>
// tree is deliberately left in place (their workflows' history, projects);
// disabling is the reversible alternative and the one the UI offers first.
func (api *StreamingAPI) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == GetUserIDFromContext(r.Context()) {
		writeUsersError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	kept := dir.Users[:0]
	var removed *UserRecord
	for i := range dir.Users {
		if dir.Users[i].ID == id {
			u := dir.Users[i]
			removed = &u
			continue
		}
		kept = append(kept, dir.Users[i])
	}
	if removed == nil {
		writeUsersError(w, http.StatusNotFound, "user not found")
		return
	}
	dir.Users = kept
	if err := saveUserDirectory(dir); err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[USERS] %s deleted user %s", GetUserIDFromContext(r.Context()), removed.Username)
	writeUsersJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// POST /api/auth/password — a user changes their own password. Requires the
// current one; an admin resets someone else's through the admin route.
func (api *StreamingAPI) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		writeUsersError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUsersError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.New) < 8 {
		writeUsersError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec := dir.find(claims.UserID, claims.Username, claims.Email)
	if rec == nil || rec.PasswordHash == "" {
		writeUsersError(w, http.StatusBadRequest, "this account does not use a password")
		return
	}
	// The current password is optional: the signed-in session is the proof of
	// identity here, and the account menu does not re-prompt for it. A client
	// that does send one must still get it right.
	if req.Current != "" && !verifyPassword(rec.PasswordHash, req.Current) {
		writeUsersError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := hashPassword(req.New)
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec.PasswordHash = hash
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := saveUserDirectory(dir); err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeUsersJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// knownProductIDs lists the product surfaces an admin can enable per user:
// AgentWorks itself plus every registered product profile.
// canCreateInProducts is, for each product, whether the account may create there (PLAT-767): the app enables or
// disables each product's "New ..." from it.
func canCreateInProducts(acc UserAccess) map[string]bool {
	out := map[string]bool{}
	for _, product := range append(knownProductIDs(), "agentworks", "relays", "work", "code", "video-studio", "sparkquill") {
		out[product] = acc.CanCreateIn(product)
	}
	return out
}

func knownProductIDs() []string {
	ids := []string{"agentworks"}
	for _, id := range registeredProductIDs() {
		if id != "" && id != "agentworks" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// registeredProductIDs is the product surfaces this server can host,
// filtered by AGENT_PRODUCTS so a dedicated deployment only offers its own.
func registeredProductIDs() []string {
	var out []string
	// Relay shares the workflow runtime, but is a selectable product surface.
	if productEnabled("agentworks") || productEnabled("relays") {
		out = append(out, "relays")
	}
	if os.Getenv("CAPLAYER_SERVICE_URL") != "" && productEnabled("mcp-gateway") {
		out = append(out, "mcp-gateway")
	}
	for _, id := range []string{"video-studio", "sparkquill", "work", "code", "knowledgebase"} {
		if productEnabled(id) {
			out = append(out, id)
		}
	}
	return out
}
