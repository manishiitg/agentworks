package server

import (
	"context"
	"encoding/json"
	"path"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

type dashboardExportOptions struct {
	SourceMode string  `json:"source_mode"`
	Dashboard  string  `json:"dashboard_id,omitempty"`
	Revision   string  `json:"revision,omitempty"`
	Theme      string  `json:"theme"`
	Width      int     `json:"viewport_width"`
	Height     int     `json:"viewport_height"`
	Duration   float64 `json:"duration_s,omitempty"`
	FPS        int     `json:"fps"`
	Document   string  `json:"-"`
	SourcePath string  `json:"-"`
}

func normalizeDashboardExport(options dashboardExportOptions, composition string) (dashboardExportOptions, string, error) {
	if options.SourceMode == "" {
		options.SourceMode = "dashboard"
		if composition != "" {
			options.SourceMode = "composition"
		}
	}
	if options.SourceMode == "composition" {
		if !validDashboardMotionPath(composition) {
			return options, "", dashboardFailure(400, "Save the separate video composition under code/motion/ and pass its composition_path.")
		}
		return options, composition, nil
	}
	if options.SourceMode != "dashboard" || composition != "" {
		return options, "", dashboardFailure(400, "Choose dashboard export, or a separate composition_path, for this video.")
	}
	if options.Theme == "" {
		options.Theme = "dark"
	}
	if options.Width == 0 {
		options.Width = 1280
	}
	if options.Height == 0 {
		options.Height = 720
	}
	if options.FPS == 0 {
		options.FPS = 30
	}
	if (options.Theme != "dark" && options.Theme != "light") || options.Width < 320 || options.Width > 3840 || options.Height < 180 || options.Height > 3840 || options.FPS < 1 || options.FPS > 60 || options.Duration < 0 || options.Duration > 600 {
		return options, "", dashboardFailure(400, "Use light or dark, a viewport up to 3840 pixels, 1–60 frames per second, and a duration up to 10 minutes.")
	}
	encoded, _ := json.Marshal(options)
	return options, "dashboard:" + string(encoded), nil
}

func resolveDashboardExportSource(ctx context.Context, target dashboardTarget, options dashboardExportOptions) (dashboardExportOptions, error) {
	if options.Dashboard == "" {
		listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "list"})
		if err != nil {
			return options, err
		}
		for _, item := range listed.Dashboards {
			if item.Managed && item.Published != "" {
				if options.Dashboard != "" {
					return options, dashboardFailure(400, "Choose which published dashboard to export by passing its dashboard_id.")
				}
				options.Dashboard = item.ID
			}
		}
		if options.Dashboard == "" {
			return options, dashboardFailure(404, "Publish a dashboard first, then export it as a video.")
		}
	}
	result, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "get", ID: options.Dashboard, Revision: options.Revision})
	if err != nil {
		return options, err
	}
	if result.Dashboard == nil || result.Dashboard.Published == "" {
		return options, dashboardFailure(404, "Publish this dashboard before exporting it.")
	}
	options.Revision = result.Dashboard.Revision
	options.Document = dashboards.Document(options.Dashboard, options.Revision)
	options.SourcePath = path.Dir(options.Document)
	return options, nil
}
