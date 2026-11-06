package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// A public link (owner, 6 Nov, decisions log, PLAT-856-509) is a frozen copy of one published dashboard or artifact that
// anyone with the link opens without signing in: dashboardStaticFiles' page and runtime, kept in app-private state under
// a random code. It has no Ask, no function buttons and no live API; it changes only when the owner updates it, and
// turning it off deletes the copy. The page is served into an opaque sandbox origin, so its scripts cannot reach a
// viewer's session or storage on this site.

const (
	dashboardPublicAction = "public_link"
	dashboardPublicState  = "dashboard-public-links"
	// One asset may be 20 MB in a static export. 25 MB holds that with the 1.7 MB runtime, is about what a phone can
	// still parse from one embedded snapshot, and bounds what an unauthenticated visitor can pull per open; the 64 MB
	// export limit is for files a Builder deploys to a host of their own.
	dashboardPublicMaxBytes = 25 << 20
	dashboardPublicMacCrew  = "A public link isn't available yet for a Crew that lives on a Mac. It is coming later."
)

// Each open is about six requests, so this is some 40 opens a minute from one address, an office included.
var dashboardPublicLimiter = &loginRateLimiter{failures: map[string]*loginFailures{}, limit: 240, window: time.Minute, lockout: time.Minute, now: time.Now}

var dashboardPublicRuntime = map[string]string{
	"report-preview.js":  "text/javascript; charset=utf-8",
	"dashboard-react.js": "text/javascript; charset=utf-8",
	"dashboard.css":      "text/css; charset=utf-8",
}

var dashboardPublicToken = regexp.MustCompile(`aw_(pat|cli|mcp)_[A-Za-z0-9_-]{16,}`)

// dashboardPublicMu orders saves and deletes; the slow capture runs outside it.
var dashboardPublicMu sync.Mutex

type dashboardPublicRecord struct {
	Code      string    `json:"code"`
	ProjectID string    `json:"project_id"`
	Root      string    `json:"root"`
	Dashboard string    `json:"dashboard_id"`
	Revision  string    `json:"revision"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Bytes     int       `json:"bytes"`
}

// dashboardPublicDialog is the confirm box before a link goes public, worded here; the screens only lay it out.
type dashboardPublicDialog struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Warning string `json:"warning"`
	Note    string `json:"note"`
	Cancel  string `json:"cancel"`
	Confirm string `json:"confirm"`
}

// dashboardPublicView is one dashboard's public link as its owner sees it, every line written here.
type dashboardPublicView struct {
	DashboardID string                 `json:"dashboard_id"`
	Title       string                 `json:"title,omitempty"`
	Artifact    bool                   `json:"artifact,omitempty"`
	Label       string                 `json:"label"`
	On          bool                   `json:"on"`
	URL         string                 `json:"url,omitempty"`
	Line        string                 `json:"line,omitempty"`
	CopyLabel   string                 `json:"copy_label,omitempty"`
	UpdateLabel string                 `json:"update_label,omitempty"`
	Dialog      *dashboardPublicDialog `json:"dialog,omitempty"`
	Blocked     string                 `json:"blocked,omitempty"`
}

func dashboardPublicDate(t time.Time) string { return t.UTC().Format("2 Jan 2006, 15:04 UTC") }

func dashboardPublicViewOf(id, title string, artifact bool, record *dashboardPublicRecord) dashboardPublicView {
	view := dashboardPublicView{DashboardID: id, Title: title, Artifact: artifact, Label: "Public link"}
	if record == nil {
		view.Dialog = &dashboardPublicDialog{
			Title:   fmt.Sprintf("Make %q public?", title),
			Body:    "Anyone with the link can open this page, without signing in.",
			Warning: "Everything on this page goes public, including the data behind it (its query results, even columns the page doesn't show).",
			Note:    "It is a frozen copy: it does not change until you update it, and nobody can ask questions or press buttons on it. Turning it off stops the link at once.",
			Cancel:  "Cancel", Confirm: "Make public",
		}
		return view
	}
	view.On, view.URL = true, effectiveShareBaseURL()+"/p/"+record.Code
	view.Line = "Anyone with this link sees this page as it was on " + dashboardPublicDate(record.UpdatedAt) + "."
	view.CopyLabel, view.UpdateLabel = "Copy", "Update it"
	return view
}

func dashboardPublicDir() (string, error) {
	root, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, dashboardPublicState), nil
}

func newDashboardPublicCode() (string, error) {
	code := make([]byte, 16)
	if _, err := rand.Read(code); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(code), nil
}

func validDashboardPublicCode(code string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(code)
	return err == nil && len(raw) == 16
}

func readDashboardPublicRecord(dir, code string) (dashboardPublicRecord, error) {
	var record dashboardPublicRecord
	data, err := os.ReadFile(filepath.Join(dir, code, "record.json"))
	if err != nil {
		return record, err
	}
	return record, json.Unmarshal(data, &record)
}

// dashboardPublicRecords are a project's public links by dashboard. A server has few, so reading the folder is enough.
func dashboardPublicRecords(root string) map[string]dashboardPublicRecord {
	records := map[string]dashboardPublicRecord{}
	dir, err := dashboardPublicDir()
	if err != nil {
		return records
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return records
	}
	for _, entry := range entries {
		if !validDashboardPublicCode(entry.Name()) {
			continue
		}
		if record, err := readDashboardPublicRecord(dir, entry.Name()); err == nil && record.Root == root {
			records[record.Dashboard] = record
		}
	}
	return records
}

// saveDashboardPublicPage swaps in a whole new copy, so a reader never gets half of an update.
func saveDashboardPublicPage(record dashboardPublicRecord, files map[string]string) error {
	dir, err := dashboardPublicDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	staged, err := os.MkdirTemp(dir, ".page-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staged, "record.json"), data, 0o600); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(staged, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	live, old := filepath.Join(dir, record.Code), staged+".old"
	if err := os.Rename(live, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	defer os.RemoveAll(old)
	return os.Rename(staged, live)
}

// dashboardPublicMainOwner: this feature is the Crew's main owner's alone, not a co-owner's or a Team member's.
func dashboardPublicMainOwner(ctx context.Context, claims *UserClaims, target dashboardTarget) bool {
	if claims == nil {
		return false
	}
	crew, ok := resolveCrewPath(ctx, claims.UserID, target.Root)
	return ok && crew.OwnerID == sanitizeUserIDForPath(claims.UserID)
}

func dashboardPublicMacCrewBlocked(id string) bool { return IsLocalMode() || crewRelayTwin(id) }

// dashboardPublicViews lists a project's published dashboards with their public links.
func dashboardPublicViews(ctx context.Context, target dashboardTarget) ([]dashboardPublicView, error) {
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "list"})
	if err != nil {
		return nil, err
	}
	records := dashboardPublicRecords(target.Root)
	views := []dashboardPublicView{}
	for _, d := range listed.Dashboards {
		if !d.Managed || d.Published == "" {
			continue
		}
		var record *dashboardPublicRecord
		if found, ok := records[d.ID]; ok {
			record = &found
		}
		views = append(views, dashboardPublicViewOf(d.ID, d.Title, d.Artifact, record))
	}
	return views, nil
}

// dashboardPublicShareViews feeds the Share screen of a Crew with its Team off: its main owner gets the same control
// the Team tab's Dashboards card has. Anyone else, or a Crew with its Team on, gets nothing here.
func dashboardPublicShareViews(ctx context.Context, claims *UserClaims, target dashboardTarget) []dashboardPublicView {
	if target.Kind != "crew" || dashboardPublicMacCrewBlocked(target.ID) || !dashboardPublicMainOwner(ctx, claims, target) {
		return nil
	}
	if team, err := readCrewTeam(target.ID); err == nil && team.On {
		return nil
	}
	views, err := dashboardPublicViews(ctx, target)
	if err != nil {
		log.Printf("[DASHBOARD_PUBLIC] list failed project=%s: %v", target.ID, err)
		return nil
	}
	return views
}

// dashboardPublicSecretIn names a secret value or sign-in token held in the page, if any. A dashboard's data can carry
// one in from a file or a database row; the secrets looked for are the ones this Crew's scripts receive. ok is false
// when those can't be read (crewSelectedSecretEnv answers nil; a Crew that selects none gets an empty map): the page
// then can't be cleared, so it does not go public.
func (api *StreamingAPI) dashboardPublicSecretIn(ctx context.Context, target dashboardTarget, owner, page string) (found string, ok bool) {
	secrets := api.crewSelectedSecretEnv(ctx, crewProfileID, target.Root, target.Root, owner)
	if secrets == nil {
		return "", false
	}
	for _, pair := range os.Environ() {
		if name, value, ok := strings.Cut(pair, "="); ok && strings.HasPrefix(name, "SECRET_") {
			secrets[name] = value
		}
	}
	for name, value := range secrets {
		if len(value) < 6 {
			continue
		}
		// The page's data is a JSON string with < escaped, so look for that spelling too.
		var encoded strings.Builder
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(value)
		inJSON := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(encoded.String()), `"`), `"`), "<", `\u003c`)
		if strings.Contains(page, value) || strings.Contains(page, inJSON) {
			return "$" + name, true
		}
	}
	if dashboardPublicToken.MatchString(page) {
		return "a sign-in token", true
	}
	return "", true
}

func (api *StreamingAPI) handleDashboardPublicLink(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, mode, id string) {
	ctx := r.Context()
	if target.Kind != "crew" {
		externalError(w, 409, "crew_only", "Public links are for Crews for now.")
		return
	}
	if !dashboardPublicMainOwner(ctx, claims, target) || !dashboardAuthoringAllowed(claims, &target) {
		log.Printf("[DASHBOARD_PUBLIC] refused action=%q actor=%s project=%s dashboard=%q reason=not_owner", mode, sanitizeUserIDForPath(claims.UserID), target.ID, id)
		externalError(w, 403, "forbidden", "Only this Crew's owner can change its public links.")
		return
	}
	if dashboardPublicMacCrewBlocked(target.ID) {
		externalError(w, 409, "mac_crew", dashboardPublicMacCrew)
		return
	}
	switch mode {
	case "status":
		views, err := dashboardPublicViews(ctx, target)
		if err != nil {
			dashboardError(w, err)
			return
		}
		externalJSON(w, map[string]any{"links": views})
	case "on", "update":
		api.saveDashboardPublicLink(w, r, claims, target, mode, id)
	case "off":
		if id == "" {
			externalError(w, 400, "invalid_arguments", "Choose the dashboard by its dashboard_id.")
			return
		}
		dashboardPublicMu.Lock()
		defer dashboardPublicMu.Unlock()
		record, found := dashboardPublicRecords(target.Root)[id]
		if found {
			dir, err := dashboardPublicDir()
			if err == nil {
				err = os.RemoveAll(filepath.Join(dir, record.Code))
			}
			if err != nil {
				log.Printf("[DASHBOARD_PUBLIC] off failed project=%s dashboard=%s: %v", target.ID, id, err)
				externalError(w, 500, "public_link_failed", "The public link could not be turned off. Try again.")
				return
			}
			log.Printf("[DASHBOARD_PUBLIC] action=off actor=%s project=%s dashboard=%s bytes=%d", sanitizeUserIDForPath(claims.UserID), target.ID, id, record.Bytes)
		}
		externalJSON(w, dashboardPublicViewOf(id, target.dashboardTitle(ctx, id), false, nil))
	default:
		externalError(w, 400, "invalid_arguments", "Use mode on, update, off or status.")
	}
}

func (target dashboardTarget) dashboardTitle(ctx context.Context, id string) string {
	result, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "get", ID: id})
	if err != nil || result.Dashboard == nil {
		return id
	}
	return result.Dashboard.Title
}

func (api *StreamingAPI) saveDashboardPublicLink(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, mode, id string) {
	if id == "" {
		externalError(w, 400, "invalid_arguments", "Choose the dashboard by its dashboard_id.")
		return
	}
	actor := sanitizeUserIDForPath(claims.UserID)
	current, found := dashboardPublicRecords(target.Root)[id]
	// Turning on a link that is on changes nothing: the copy moves only on Update.
	if mode == "on" && found {
		externalJSON(w, dashboardPublicViewOf(id, target.dashboardTitle(r.Context(), id), false, &current))
		return
	}
	if mode == "update" && !found {
		externalError(w, 409, "public_link_off", "Turn the public link on first.")
		return
	}
	if effectiveShareBaseURL() == "" {
		externalError(w, 503, "public_url_missing", "This server has no public address yet, so it can't make a public link.")
		return
	}
	now := time.Now()
	options, files, err := api.dashboardStaticFiles(r.Context(), claims, target, dashboardExportOptions{Dashboard: id, Theme: "light"}, dashboardStaticPage{Footer: "As of " + dashboardPublicDate(now) + " · Made with AgentWorks"})
	if err != nil {
		dashboardError(w, err)
		return
	}
	secret, checked := api.dashboardPublicSecretIn(r.Context(), target, claims.UserID, files["index.html"])
	if !checked {
		log.Printf("[DASHBOARD_PUBLIC] refused action=%s actor=%s project=%s dashboard=%s reason=secrets_unavailable", mode, actor, target.ID, id)
		externalError(w, 409, "secrets_unavailable", "This Crew's secrets can't be checked right now, so nothing was made public. Check the secrets this Crew uses, then try again.")
		return
	}
	if secret != "" {
		log.Printf("[DASHBOARD_PUBLIC] refused action=%s actor=%s project=%s dashboard=%s reason=secret_in_page", mode, actor, target.ID, id)
		externalError(w, 422, "secret_in_page", "This page holds the value of "+secret+", so it can't be made public. Take that value off the page, publish it again and retry.")
		return
	}
	size := 0
	for _, content := range files {
		size += len(content)
	}
	if size > dashboardPublicMaxBytes {
		log.Printf("[DASHBOARD_PUBLIC] refused action=%s actor=%s project=%s dashboard=%s bytes=%d reason=too_large", mode, actor, target.ID, id, size)
		externalError(w, 422, "too_large", fmt.Sprintf("This page is %d MB, over the %d MB limit for a public link. Use fewer or smaller images, then try again.", size>>20, dashboardPublicMaxBytes>>20))
		return
	}
	dashboardPublicMu.Lock()
	defer dashboardPublicMu.Unlock()
	// The capture took a while: read the link again, so an Off in between wins and two Ons share one code.
	current, found = dashboardPublicRecords(target.Root)[id]
	if mode == "update" && !found {
		externalError(w, 409, "public_link_off", "This public link was turned off.")
		return
	}
	record := dashboardPublicRecord{Code: current.Code, ProjectID: target.ID, Root: target.Root, Dashboard: id, Revision: options.Revision, Actor: actor, CreatedAt: current.CreatedAt, UpdatedAt: now, Bytes: size}
	if !found {
		if record.Code, err = newDashboardPublicCode(); err != nil {
			dashboardError(w, err)
			return
		}
		record.CreatedAt = now
	}
	// The runtime sits beside the page; the page's address has no folder, so name it in full.
	const relative = `<script src="./report-preview.js"></script>`
	if !strings.Contains(files["index.html"], relative) {
		externalError(w, 422, "export_failed", "The dashboard could not be frozen into a public page.")
		return
	}
	files["index.html"] = strings.ReplaceAll(files["index.html"], relative, `<script src="/p/`+record.Code+`/report-preview.js"></script>`)
	if err := saveDashboardPublicPage(record, files); err != nil {
		log.Printf("[DASHBOARD_PUBLIC] save failed project=%s dashboard=%s: %v", target.ID, id, err)
		externalError(w, 500, "public_link_failed", "The public page could not be saved. Try again.")
		return
	}
	log.Printf("[DASHBOARD_PUBLIC] action=%s actor=%s project=%s dashboard=%s bytes=%d", mode, actor, target.ID, id, size)
	externalJSON(w, dashboardPublicViewOf(id, options.Title, false, &record))
}

func (api *StreamingAPI) registerDashboardPublicRoutes(router *mux.Router) {
	router.HandleFunc("/p/{code}", api.handleDashboardPublicPage).Methods(http.MethodGet)
	router.HandleFunc("/p/{code}/{file}", api.handleDashboardPublicPage).Methods(http.MethodGet)
}

// dashboardPublicCSP: "sandbox allow-scripts" without allow-same-origin gives the page an opaque origin. The runtime and
// the page's styles load only from this site's address for the page; everything else on it is inline or embedded, and
// the document frame it renders in is a data: URL. 'self' still means this site under the sandbox; the full address is
// listed as well for browsers that read it differently.
func dashboardPublicCSP(code string) string {
	source := "'self' 'unsafe-inline'"
	if base := effectiveShareBaseURL(); base != "" {
		source += " " + base + "/p/" + code + "/"
	}
	return "sandbox allow-scripts; default-src 'none'; script-src " + source + "; style-src " + source + "; img-src data: blob:; font-src 'self' data: blob:; media-src data: blob:; frame-src data:; connect-src 'none'; form-action 'none'"
}

// handleDashboardPublicPage is GET /p/{code} and its runtime files, outside sign-in: the random code is the only key and
// it opens this frozen copy, nothing else. Every refusal is the same plain 404.
func (api *StreamingAPI) handleDashboardPublicPage(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Del("Set-Cookie")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Robots-Tag", "noindex")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "no-store")
	ip := clientIPOf(r)
	if !dashboardPublicLimiter.allow(ip) {
		http.Error(w, "Too many requests. Wait a minute and try again.", http.StatusTooManyRequests)
		return
	}
	dashboardPublicLimiter.fail(ip)
	vars := mux.Vars(r)
	code, name := vars["code"], vars["file"]
	dir, err := dashboardPublicDir()
	if err != nil || !validDashboardPublicCode(code) {
		http.NotFound(w, r)
		return
	}
	record, err := readDashboardPublicRecord(dir, code)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if dashboardPublicProjectGone(record.Root) {
		dashboardPublicMu.Lock()
		err := os.RemoveAll(filepath.Join(dir, code))
		dashboardPublicMu.Unlock()
		log.Printf("[DASHBOARD_PUBLIC] action=removed reason=crew_gone project=%s dashboard=%s bytes=%d err=%v", record.ProjectID, record.Dashboard, record.Bytes, err)
		http.NotFound(w, r)
		return
	}
	contentType := "text/html; charset=utf-8"
	if name != "" {
		if contentType = dashboardPublicRuntime[name]; contentType == "" {
			http.NotFound(w, r)
			return
		}
		header.Set("Cache-Control", "no-cache") // no data in these; the check keeps Off and Update immediate
	} else {
		name = "index.html"
		header.Set("Content-Security-Policy", dashboardPublicCSP(code))
	}
	file, err := os.Open(filepath.Join(dir, code, name))
	if err != nil {
		header.Del("Content-Security-Policy")
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	header.Set("Content-Type", contentType)
	http.ServeContent(w, r, "", info.ModTime(), file)
}

// A deleted Crew takes its public pages with it, on the first request after it went. A registry that can't be read says
// nothing about the Crew, so that never deletes a page.
func dashboardPublicProjectGone(root string) bool {
	id, ok := projectIdentityOf(root)
	if !ok {
		return true
	}
	projects, err := defaultProjectOwners().All()
	if err != nil {
		return false
	}
	_, found := projects[projectOwnerKey(id.Product, id.Folder)]
	return !found
}
