// Package dashboardmotion renders authored, snapshot-backed compositions.
// It has no dependency on a product or on the dashboard's live data bridge.
package dashboardmotion

import _ "embed"

// RendererVersion changes only with a code revision and a real rendering check.
const RendererVersion = "0.8.99"

// Runner is executed inside the caller's existing workspace sandbox.
//
//go:embed render.py
var Runner string
