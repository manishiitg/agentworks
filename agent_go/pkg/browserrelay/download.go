package browserrelay

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
)

//go:embed extension.zip
var ExtensionZip []byte

var latestExtensionVersion = sync.OnceValue(func() string {
	archive, err := zip.NewReader(bytes.NewReader(ExtensionZip), int64(len(ExtensionZip)))
	if err != nil {
		return ""
	}
	for _, file := range archive.File {
		if file.Name != "manifest.json" {
			continue
		}
		r, err := file.Open()
		if err != nil {
			return ""
		}
		defer r.Close()
		var manifest struct {
			Version string `json:"version"`
		}
		raw, err := io.ReadAll(io.LimitReader(r, 64<<10))
		if err != nil || json.Unmarshal(raw, &manifest) != nil || !validExtensionVersion(manifest.Version) {
			return ""
		}
		return manifest.Version
	}
	return ""
})

// LatestExtensionVersion is the Browser Bridge version in the download this
// server ships, so the browser panel can tell a user their extension is old.
func LatestExtensionVersion() string { return latestExtensionVersion() }

// validExtensionVersion accepts only a short dotted number, the same shape the
// diagnostics log accepts; anything else from the extension is ignored.
func validExtensionVersion(v string) bool {
	if v == "" || len(v) > 20 {
		return false
	}
	for _, c := range v {
		if c != '.' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// DownloadURL uses the public app origin, never the private CDP relay endpoint.
// Local installations without a public origin use the same-origin app path.
func DownloadURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/") + "/api/downloads/chrome-extension.zip"
}

func ExtensionUpdateMessage() string {
	return "Download the new Browser Bridge from this page: " + DownloadURL()
}
