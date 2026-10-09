package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// preview_report renders a workflow HTML document under db/reports/ in a headless
// browser through the same host runtime the Report tab uses, and reports what
// a reviewer would otherwise only learn by opening the tab: did the page
// settle, did its script throw, which tabs exist, which "Loading…"
// placeholders never got replaced, how tall it is -- plus screenshots per
// theme and width the agent can read_image. validate_report_html is the fast
// static/SQL check; this is the slow, true-render one.

const (
	reportPreviewSettleTimeout = 20 * time.Second
	reportPreviewPollInterval  = 500 * time.Millisecond
	reportPreviewDesktopWidth  = 1280
	reportPreviewTabletWidth   = 768
	reportPreviewMobileWidth   = 480
	reportPreviewScreenshotDir = "db/reports/preview"
)

type reportPreviewSnapshot struct {
	PreviewState  string   `json:"previewState"`
	Theme         string   `json:"theme"`
	Width         float64  `json:"width"`
	OpenedFiles   []string `json:"openedFiles"`
	ConsoleErrors []string `json:"consoleErrors"`
	FetchErrors   []string `json:"fetchErrors"`
	Report        struct {
		State         string   `json:"state"`
		Errors        []string `json:"errors"`
		Title         string   `json:"title"`
		Tabs          []string `json:"tabs"`
		LoadingTexts  []string `json:"loadingTexts"`
		MarkdownTexts []string `json:"markdownTexts"`
		Height        float64  `json:"height"`
	} `json:"report"`
}

type reportPreviewScreenshot struct {
	Theme string `json:"theme"`
	Width int    `json:"width"`
	Path  string `json:"path"`
	Error string `json:"error,omitempty"`
	URL   string `json:"url,omitempty"`
}

// reportPreviewScreenshotPaths returns the workspace-rooted destination for
// the browser artifact transfer and the workflow-relative path reported back.
// The destination must always carry the workspace prefix:
// FinalizeBrowserArtifact joins relative paths to the docs root, not to the
// session working directory, so an unprefixed path would resolve under a
// root-level db/reports/preview outside every workflow-scoped write guard.
func reportPreviewScreenshotPaths(workspacePath, documentPath, theme, width string) (destination, reported string) {
	dir := reportPreviewScreenshotDir
	if documentPath != "db/reports/index.html" {
		slug := strings.TrimSuffix(strings.TrimPrefix(documentPath, "db/reports/"), ".html")
		slug = strings.NewReplacer("/", "-", " ", "-").Replace(slug)
		dir += "/" + slug
	}
	name := theme + "-" + width + ".png"
	return workspacePath + "/" + dir + "/" + name, dir + "/" + name
}

// registerReportPreviewTool adds preview_report for a Workshop session.
func (api *StreamingAPI) registerReportPreviewTool(registrar definitionToolRegistrar, sessionID, userID, workspacePath string) error {
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"document_path": map[string]interface{}{
				"type":        "string",
				"description": "Report document under db/reports/ to preview. Defaults to db/reports/index.html.",
			},
			"theme": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"dark", "light", "both"},
				"description": "Which app theme(s) to render and screenshot. Default both.",
			},
			"width": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"tablet", "mobile", "desktop", "all", "both"},
				"description": "Viewport width(s): tablet (768px, primary), mobile (480px), desktop (1280px), or all. Default all. Legacy both means desktop + mobile.",
			},
		},
		"additionalProperties": false,
	}
	description := "Render one HTML document under db/reports/ in a headless browser exactly as the Report tab does and return the real outcome: settled/error state, script errors, failed data reads, tab labels, stale loading text, page height, and screenshots. document_path defaults to db/reports/index.html. Run after validate_report_html passes; it is slower (~10-30s) and needs a browser."
	return registrar.RegisterCustomToolWithTimeout(
		"preview_report",
		description,
		params,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return api.runReportPreview(ctx, sessionID, userID, workspacePath, args)
		},
		3*time.Minute,
		"workflow",
	)
}

func reportPreviewWidthChoices(value interface{}) []string {
	choice := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch choice {
	case "", "all", "<nil>":
		return []string{"tablet", "mobile", "desktop"}
	case "both":
		return []string{"desktop", "mobile"}
	case "tablet", "mobile", "desktop":
		return []string{choice}
	default:
		return []string{"tablet", "mobile", "desktop"}
	}
}

func reportPreviewWidthPixels(width string) int {
	switch width {
	case "mobile":
		return reportPreviewMobileWidth
	case "desktop":
		return reportPreviewDesktopWidth
	default:
		return reportPreviewTabletWidth
	}
}

func reportPreviewChoices(value interface{}, both []string) []string {
	choice := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch choice {
	case "", "both", "<nil>":
		return both
	}
	for _, option := range both {
		if option == choice {
			return []string{choice}
		}
	}
	return both
}

func (api *StreamingAPI) runReportPreview(ctx context.Context, sessionID, userID, workspacePath string, args map[string]interface{}) (string, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return "", fmt.Errorf("preview_report needs a workflow workspace")
	}
	documentPath, err := cleanReportDocumentPath(args["document_path"])
	if err != nil {
		return "", err
	}
	themes := reportPreviewChoices(args["theme"], []string{"dark", "light"})
	widths := reportPreviewWidthChoices(args["width"])

	previewClaims := GetUserFromContext(ctx)
	if previewClaims == nil {
		previewClaims = &UserClaims{UserID: userID, Username: userID}
	}
	token, err := mintReportPreviewToken(previewClaims, workspacePath)
	if err != nil {
		return "", fmt.Errorf("mint preview token: %w", err)
	}
	pageURL := fmt.Sprintf("%s%s?workspace=%s&token=%s&theme=%s&width=%d&document=%s",
		strings.TrimRight(api.GetCodeExecAPIURL(), "/"), reportPreviewPagePath,
		url.QueryEscape(workspacePath), url.QueryEscape(token), themes[0], reportPreviewTabletWidth, url.QueryEscape(documentPath))

	// Always headless and always its own session: the preview must never take
	// over the user's CDP Chrome or a browser session the workflow is using.
	executor := browser.NewExecutor(
		browser.NewClient(getWorkspaceAPIURL()),
		browser.WithBrowserRuntimeConfig(browser.NewBrowserRuntimeConfig("headless", nil)),
	)
	browserCtx := context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	browserCtx = context.WithValue(browserCtx, common.WorkflowSessionIDKey, sessionID)
	previewID := uuid.NewString()
	session := "report-preview-" + previewID
	run := func(command string, cmdArgs ...string) (string, error) {
		return executor.HandleAgentBrowser(browserCtx, map[string]interface{}{
			"command": command,
			"args":    cmdArgs,
			"session": session,
		})
	}
	eval := func(js string) (string, error) {
		out, err := run("eval", js)
		if err != nil {
			return "", err
		}
		return parseBrowserEvalOutput(out)
	}
	defer func() {
		if _, closeErr := run("close"); closeErr != nil {
			log.Printf("[REPORT_PREVIEW] close session %s: %v", session, closeErr)
		}
	}()

	if _, err := run("open", pageURL); err != nil {
		return "", fmt.Errorf("open preview page: %w", err)
	}

	// Wait for the page to settle: the host runtime marks the document ready
	// once every report.ready() callback and replayed early call has settled,
	// or error as soon as the report's script throws.
	state := "loading"
	deadline := time.Now().Add(reportPreviewSettleTimeout)
	for time.Now().Before(deadline) {
		current, err := eval("document.documentElement.getAttribute('data-preview-state') || 'loading'")
		if err == nil {
			state = strings.Trim(strings.TrimSpace(current), `"`)
		}
		if state == "ready" || state == "error" || state == "missing" || state == "failed" {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(reportPreviewPollInterval):
		}
	}

	var snapshot reportPreviewSnapshot
	if raw, err := eval("JSON.stringify(window.__reportPreview ? window.__reportPreview.getState() : {previewState:'failed'})"); err == nil {
		if jsonErr := json.Unmarshal([]byte(raw), &snapshot); jsonErr != nil {
			log.Printf("[REPORT_PREVIEW] could not parse preview state %q: %v", truncateForLog(raw, 200), jsonErr)
		}
	}
	if snapshot.PreviewState == "" {
		snapshot.PreviewState = state
	}

	// The report is closed to screenshots when there is nothing to show.
	screenshots := []reportPreviewScreenshot{}
	if snapshot.PreviewState == "ready" || snapshot.PreviewState == "error" {
		for _, theme := range themes {
			if _, err := eval(fmt.Sprintf("window.__reportPreview.setTheme(%q)", theme)); err != nil {
				log.Printf("[REPORT_PREVIEW] set theme %s: %v", theme, err)
			}
			for _, width := range widths {
				px := reportPreviewWidthPixels(width)
				if _, err := eval(fmt.Sprintf("window.__reportPreview.setWidth(%d)", px)); err != nil {
					log.Printf("[REPORT_PREVIEW] set width %d: %v", px, err)
				}
				time.Sleep(400 * time.Millisecond)
				destination, reported := reportPreviewScreenshotPaths(workspacePath, documentPath, theme, width)
				reported = path.Join(path.Dir(reported), previewID, path.Base(reported))
				destination = workspacePath + "/" + reported
				shot := reportPreviewScreenshot{Theme: theme, Width: px, Path: reported, URL: strings.TrimRight(api.GetCodeExecAPIURL(), "/") + "/api/workflow/report-preview/file?workspace=" + url.QueryEscape(workspacePath) + "&path=" + url.QueryEscape(reported)}
				if _, err := run("screenshot", destination, "--full"); err != nil {
					// Retry without the full-page flag in case this CLI build rejects it.
					if _, retryErr := run("screenshot", destination); retryErr != nil {
						shot.Error = retryErr.Error()
					}
				}
				screenshots = append(screenshots, shot)
			}
		}
	}

	summary := reportPreviewSummary(snapshot, screenshots, documentPath)
	result := map[string]interface{}{
		"document_path":  documentPath,
		"state":          snapshot.PreviewState,
		"summary":        summary,
		"title":          snapshot.Report.Title,
		"report_state":   snapshot.Report.State,
		"script_errors":  nonNilStrings(snapshot.Report.Errors),
		"page_errors":    nonNilStrings(snapshot.ConsoleErrors),
		"fetch_errors":   nonNilStrings(snapshot.FetchErrors),
		"tabs":           nonNilStrings(snapshot.Report.Tabs),
		"loading_texts":  nonNilStrings(snapshot.Report.LoadingTexts),
		"markdown_texts": nonNilStrings(snapshot.Report.MarkdownTexts),
		"opened_files":   nonNilStrings(snapshot.OpenedFiles),
		"height_px":      snapshot.Report.Height,
		"screenshots":    screenshots,
		"rendered_via":   "headless browser, same report host runtime as the Report tab",
		"next_step":      "Open each screenshot with read_image and judge layout, contrast and empty states; fix every script/fetch error and stale 'Loading…' placeholder.",
		"page_url":       strings.Split(pageURL, "&token=")[0],
		"settle_timeout": reportPreviewSettleTimeout.String(),
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func cleanReportDocumentPath(value interface{}) (string, error) {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return "db/reports/index.html", nil
	}
	if raw != strings.ReplaceAll(raw, "\\", "/") || !strings.HasPrefix(raw, "db/reports/") || !strings.HasSuffix(strings.ToLower(raw), ".html") {
		return "", fmt.Errorf("document_path must be a canonical .html path under db/reports/")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("document_path must be a canonical .html path under db/reports/")
		}
	}
	return raw, nil
}

func reportPreviewSummary(s reportPreviewSnapshot, shots []reportPreviewScreenshot, documentPath string) string {
	switch s.PreviewState {
	case "missing":
		return fmt.Sprintf("No %s exists for this workspace.", documentPath)
	case "failed":
		return "The preview page itself could not load the report (see fetch_errors)."
	case "loading":
		return fmt.Sprintf("The report never settled within %s: its ready()/report:data work is still pending or hangs. Check for an await that never resolves or a query that never returns.", reportPreviewSettleTimeout)
	case "ready", "error":
		// These are the only states for which the preview attempts screenshots.
	default:
		return fmt.Sprintf("The browser returned an unrecognized preview state %q; rendering was not verified.", s.PreviewState)
	}
	parts := []string{}
	if s.PreviewState == "error" || len(s.Report.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("the report's script threw (%d error(s))", len(s.Report.Errors)))
	}
	if len(s.FetchErrors) > 0 {
		parts = append(parts, fmt.Sprintf("%d data read(s) failed", len(s.FetchErrors)))
	}
	if len(s.Report.LoadingTexts) > 0 {
		parts = append(parts, fmt.Sprintf("%d 'Loading…' placeholder(s) never replaced", len(s.Report.LoadingTexts)))
	}
	if len(s.Report.MarkdownTexts) > 0 {
		parts = append(parts, fmt.Sprintf("%d text node(s) look like unrendered markdown; pass agent-written markdown through window.report.renderMarkdown", len(s.Report.MarkdownTexts)))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Rendered cleanly: %d tab label(s), %.0fpx tall, %d screenshot(s).", len(s.Report.Tabs), s.Report.Height, len(shots))
	}
	return "Rendered with problems: " + strings.Join(parts, "; ") + "."
}

// parseBrowserEvalOutput unwraps agent-browser's --json envelope. Current
// agent-browser versions return eval values as
// {"success":true,"data":{"result":...}}, not as a bare JSON string. Treating
// that whole envelope as the evaluated value made the preview poll for 20
// seconds, capture no screenshots, and then fall through to "Rendered
// cleanly." even when data.result was "failed".
func parseBrowserEvalOutput(out string) (string, error) {
	trimmed := strings.TrimSpace(out)
	var envelope struct {
		Success *bool `json:"success"`
		Data    struct {
			Result json.RawMessage `json:"result"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err == nil && envelope.Success != nil {
		if !*envelope.Success {
			detail := strings.TrimSpace(string(envelope.Error))
			if detail == "" || detail == "null" {
				detail = "unknown error"
			}
			return "", fmt.Errorf("agent-browser eval failed: %s", detail)
		}
		if len(envelope.Data.Result) == 0 || string(envelope.Data.Result) == "null" {
			return "", fmt.Errorf("agent-browser eval returned no result")
		}
		var asString string
		if err := json.Unmarshal(envelope.Data.Result, &asString); err == nil {
			return asString, nil
		}
		return strings.TrimSpace(string(envelope.Data.Result)), nil
	}
	return unquoteBrowserEvalOutput(trimmed), nil
}

// unquoteBrowserEvalOutput: agent-browser prints eval results as JSON, so a
// string result arrives quoted (and JSON.stringify output double-encoded).
func unquoteBrowserEvalOutput(out string) string {
	trimmed := strings.TrimSpace(out)
	var asString string
	if err := json.Unmarshal([]byte(trimmed), &asString); err == nil {
		return asString
	}
	return trimmed
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
