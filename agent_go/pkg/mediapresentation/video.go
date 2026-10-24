package mediapresentation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/presentations"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"net/url"
	"path/filepath"
	"strings"
)

var videoExtensions = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".m4v": true}

type qualityReportCheck struct {
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

type qualityReportFrame struct {
	TimestampSeconds float64 `json:"timestamp_seconds"`
	Path             string  `json:"path"`
}

type qualityReport struct {
	SchemaVersion     int                           `json:"schema_version"`
	CandidatePath     string                        `json:"candidate_path"`
	ContactSheetPath  string                        `json:"contact_sheet_path"`
	Verdict           string                        `json:"verdict"`
	ReadyToPresent    bool                          `json:"ready_to_present"`
	Checks            map[string]qualityReportCheck `json:"checks"`
	SampledFrames     []qualityReportFrame          `json:"sampled_frames"`
	RecommendedAction string                        `json:"recommended_action"`
}

func profileWorkspaceRoot(userID, workspacePath string) string {
	ref, ok := workspaceref.Parse(workspacePath)
	if !ok {
		// A path escaping the workspace maps to a folder that does not exist,
		// never to the user's whole tree.
		return workspaceref.PhysicalPath(userID, "_invalid_workspace_path")
	}
	return ref.PhysicalKeepOwner(strings.TrimSpace(userID))
}

func cleanProjectPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is missing")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(raw, "/")))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path must stay inside the project")
	}
	return filepath.ToSlash(clean), nil
}
func resolveWorkspaceEvidence(ctx context.Context, client *workspace.Client, projectRoot, rawPath, reportPath string) (string, []byte, error) {
	clean, err := cleanProjectPath(rawPath)
	if err != nil {
		return "", nil, err
	}
	read := func(relative string) ([]byte, error) {
		return client.DownloadFile(ctx, filepath.ToSlash(filepath.Join(projectRoot, relative)))
	}
	if data, err := read(clean); err == nil && len(data) > 0 {
		return clean, data, nil
	}
	executionRoot := filepath.ToSlash(filepath.Dir(filepath.Dir(filepath.FromSlash(reportPath))))
	if filepath.Base(executionRoot) != "execution" {
		return clean, nil, fmt.Errorf("file is missing or empty: %s", clean)
	}
	resolved, err := cleanProjectPath(filepath.ToSlash(filepath.Join(executionRoot, filepath.FromSlash(clean))))
	if err != nil {
		return "", nil, err
	}
	data, err := read(resolved)
	if err != nil || len(data) == 0 {
		return resolved, nil, fmt.Errorf("file is missing or empty: %s", resolved)
	}
	return resolved, data, nil
}

func validateWorkspaceQualityReport(ctx context.Context, client *workspace.Client, projectRoot, videoPath, rawReportPath, note string) (string, error) {
	reportPath, err := cleanProjectPath(rawReportPath)
	if err != nil || filepath.Base(reportPath) != "quality-report.json" {
		return "", errors.New("QA report must be a project-relative quality-report.json")
	}
	reportFile, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: filepath.ToSlash(filepath.Join(projectRoot, reportPath))})
	if err != nil {
		return "", errors.New("the required quality-report.json does not exist")
	}
	var report qualityReport
	if err := json.Unmarshal([]byte(reportFile.Content), &report); err != nil || report.SchemaVersion != 1 {
		return "", errors.New("quality-report.json is invalid or unsupported")
	}
	candidatePath, candidate, err := resolveWorkspaceEvidence(ctx, client, projectRoot, report.CandidatePath, reportPath)
	if err != nil || candidatePath != videoPath || len(candidate) == 0 {
		return "", errors.New("the QA report names a different or missing video")
	}
	if !report.ReadyToPresent || (report.Verdict != "pass" && report.Verdict != "placeholder-pass") || report.RecommendedAction != "present" {
		return "", errors.New("the QA verdict is not ready to present")
	}
	if report.Verdict == "placeholder-pass" && !strings.Contains(strings.ToLower(note), "placeholder") {
		return "", errors.New("a placeholder-pass must be clearly labelled as a placeholder")
	}
	for _, name := range []string{"technical", "visual", "audio", "content", "captions", "promise"} {
		check, ok := report.Checks[name]
		if !ok || (check.Status != "pass" && check.Status != "not_applicable") || len(check.Evidence) == 0 {
			return "", fmt.Errorf("the %s check is not supported by passing evidence", name)
		}
	}
	// generated-video-quality's checks are not required for every candidate --
	// the infographic route never generates footage and has nothing to check
	// here. But a report that includes one of these names is claiming that
	// check was actually run, and the same evidence discipline applies: a
	// fabricated "pass" with no evidence is rejected exactly like a missing
	// base check would be. This does not force the check to exist; it refuses
	// to trust it when it claims to.
	for _, name := range []string{
		"character_consistency", "generation_artifacts", "temporal_coherence",
		"motion_plausibility", "lip_sync", "clip_color_consistency",
		"prompt_adherence", "narration_alignment",
	} {
		check, ok := report.Checks[name]
		if !ok {
			continue
		}
		if (check.Status != "pass" && check.Status != "not_applicable") || len(check.Evidence) == 0 {
			return "", fmt.Errorf("the %s check is not supported by passing evidence", name)
		}
	}
	if len(report.SampledFrames) < 4 {
		return "", errors.New("fewer than four inspected frames were recorded")
	}
	if _, data, err := resolveWorkspaceEvidence(ctx, client, projectRoot, report.ContactSheetPath, reportPath); err != nil || len(data) == 0 {
		return "", errors.New("the QA contact sheet is missing or empty")
	}
	for _, frame := range report.SampledFrames {
		if _, data, err := resolveWorkspaceEvidence(ctx, client, projectRoot, frame.Path, reportPath); err != nil || len(data) == 0 {
			return "", errors.New("an inspected frame is missing or empty")
		}
	}
	return report.Verdict, nil
}

func showVideoFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		projectRoot := profileWorkspaceRoot(runtime.UserID, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "show_video", Category: "presentation_tools",
			Description: "Present a playable project video in the Production panel. Call this as soon as a reviewable MP4 exists; include source_url when Fal returned its HTTPS CDN URL so the panel can start from that faster source and fall back to the durable project file. Include a passing quality-report.json only when marking it as an approved final. Repeating the same path updates the existing presentation.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":           map[string]interface{}{"type": "string", "description": "Project-relative path to the exact video"},
				"source_url":     map[string]interface{}{"type": "string", "description": "Optional HTTPS Fal CDN URL for fast preview playback; the project file remains required."},
				"qa_report_path": map[string]interface{}{"type": "string", "description": "Optional project-relative passing quality-report.json. Omit for a review preview."},
				"title":          map[string]interface{}{"type": "string"},
				"note":           map[string]interface{}{"type": "string"},
			}, "required": []string{"path", "title"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				// The profile must declare this tool's presentation kind in
				// product.yaml (tools[].presentation.kind). Requiring it here,
				// rather than falling back to a hardcoded "media.video",
				// makes the YAML declaration load-bearing instead of
				// decorative -- the same reason tool_policy.enabled has to be
				// consulted rather than optional.
				if runtime.Presentation == nil || strings.TrimSpace(runtime.Presentation.Kind) == "" || presentationActivity(runtime.Presentation) == nil {
					return "", fmt.Errorf("show_video: profile did not declare a presentation kind and activity for this tool")
				}
				rawPath, _ := args["path"].(string)
				videoPath, err := cleanProjectPath(rawPath)
				if err != nil || !videoExtensions[strings.ToLower(filepath.Ext(videoPath))] {
					return "show_video requires a project-relative video path.", nil
				}
				reportPath, _ := args["qa_report_path"].(string)
				sourceURL, err := falCDNVideoURL(stringArg(args, "source_url"))
				if err != nil {
					return "show_video source_url must be an HTTPS URL hosted on Fal's CDN.", nil
				}
				title, _ := args["title"].(string)
				note, _ := args["note"].(string)
				title = strings.TrimSpace(title)
				if title == "" {
					title = filepath.Base(videoPath)
				}
				verdict := "preview"
				if strings.TrimSpace(reportPath) != "" {
					verdict, err = validateWorkspaceQualityReport(ctx, client, projectRoot, videoPath, reportPath, note)
					if err != nil {
						return "Quality assurance has not passed for this video: " + err.Error(), nil
					}
				} else if _, data, err := resolveWorkspaceEvidence(ctx, client, projectRoot, videoPath, videoPath); err != nil || len(data) == 0 {
					return "show_video requires an existing, non-empty project video: " + videoPath, nil
				}
				event, err := persistPresentation(ctx, client, presentations.Presentation{
					Kind:          runtime.Presentation.Kind,
					IdentityKey:   videoPath,
					Title:         title,
					WorkspacePath: runtime.WorkspacePath,
					SessionID:     runtime.SessionID,
					Activity:      presentationActivity(runtime.Presentation),
					Payload: map[string]interface{}{
						"path": videoPath, "qa_report_path": reportPath,
						"source_url": sourceURL, "note": strings.TrimSpace(note), "verdict": verdict,
					},
					Resources: []map[string]string{{"kind": "workspace.file", "path": videoPath, "role": "primary"}},
				})
				if err != nil {
					return "", fmt.Errorf("persist video presentation: %w", err)
				}
				if runtime.Emit != nil {
					runtime.Emit(&event)
				}
				return fmt.Sprintf("Showing %q in the Videos panel.", title), nil
			},
		}, nil
	}
}

// falCDNVideoURL keeps presentation playback on an expected, HTTPS-only
// provider origin. The local project MP4 remains mandatory for durable review,
// QA, download, and a fallback if Fal's temporary CDN URL has expired.
func falCDNVideoURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("invalid Fal CDN URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "fal.media" && !strings.HasSuffix(host, ".fal.media") {
		return "", errors.New("source is not hosted by Fal CDN")
	}
	return parsed.String(), nil
}
