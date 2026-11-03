// Package sqlitefile builds SQLite file: URIs that work on every OS.
package sqlitefile

import (
	"net/url"
	"path/filepath"
	"strings"
)

// URI returns the file: URI for an absolute path, without query. A Windows path
// becomes file:///C:/dir/x.sqlite; "file:C:%5Cdir..." is refused by SQLite
// ("invalid uri authority"), which stopped the Windows app's server at start.
func URI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
