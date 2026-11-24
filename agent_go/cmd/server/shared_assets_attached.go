package server

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
)

// serveAttachedFolderAsset shows a file that sits in one of the viewer's own
// attached folders (the folders their chats get as WORK_FOLDER_<ALIAS>), so an
// agent can put a screenshot from such a folder straight into chat (#3201).
// Grants belong to the signed-in viewer, never to the chat's author: a
// teammate reading a shared chat sees the file only if it is in a folder they
// attached themselves. Anything else is refused with what to do instead.
func serveAttachedFolderAsset(w http.ResponseWriter, r *http.Request, full, operation string) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "unauthorized", "Sign in to view this file.")
		return
	}
	outside := func() {
		externalError(w, 403, "outside_attached_folders", "This file is outside the folders attached to your chats. Attach its folder, or ask the agent to copy it into the project.")
	}
	// Account folders are a signed-in person's grant, not an access token's.
	if claims.AccessToken != nil || operation != "read" {
		outside()
		return
	}
	target, err := filepath.EvalSymlinks(full)
	if err != nil {
		externalError(w, 404, "not_found", "File not found.")
		return
	}
	grants, _ := workFolderAccessForClaims(r.Context(), claims)
	for _, grant := range workproduct.AvailableAttachedGrants(grants) {
		root, e := filepath.EvalSymlinks(grant.Path)
		if e != nil || !workproduct.PathWithinRoots(target, []string{root}) || target == root {
			continue
		}
		rel, e := filepath.Rel(root, target)
		if e != nil {
			continue
		}
		// Open through the root so a symlink swapped in after the check cannot escape it.
		dir, e := os.OpenRoot(root)
		if e != nil {
			continue
		}
		defer dir.Close()
		f, e := dir.Open(rel)
		if e != nil {
			externalError(w, 404, "not_found", "File not found.")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			externalError(w, 404, "not_found", "Choose a file, not a folder.")
			return
		}
		name := filepath.Base(rel)
		if kind := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); kind != "" {
			w.Header().Set("Content-Type", kind)
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// Same as serveSharedAsset: HTML/SVG must not run with the app's origin.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data: blob:")
		disposition := "inline"
		if r.URL.Query().Get("download") == "true" {
			disposition = "attachment"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
		http.ServeContent(w, r, name, info.ModTime(), f)
		return
	}
	outside()
}
