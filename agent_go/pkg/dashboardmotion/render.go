// Package dashboardmotion renders authored, snapshot-backed compositions.
// It has no dependency on a product or on the dashboard's live data bridge.
package dashboardmotion

import (
	_ "embed"
	"encoding/base64"
	"strings"
)

// RendererVersion changes only with a code revision and a real rendering check.
const RendererVersion = "0.8.99"

// Runner is executed inside the caller's existing workspace sandbox.
//
//go:embed render.py
var Runner string

//go:embed capture.mjs
var CaptureDriver string

// Command carries non-secret render configuration as data, independently of
// the workspace shell's intentionally restricted environment-key list.
func Command(config []byte) string {
	runner := "CAPTURE_DRIVER = __import__('base64').b64decode('" + base64.StdEncoding.EncodeToString([]byte(CaptureDriver)) + "').decode()\n" + Runner
	return "python3 -c '" + strings.ReplaceAll(runner, "'", "'\\''") + "' '" + base64.StdEncoding.EncodeToString(config) + "'"
}
