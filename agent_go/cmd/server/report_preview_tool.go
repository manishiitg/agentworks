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
	reportPreviewMobileWidth   = 390
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

// Each check belongs to the same actual viewport/theme as its screenshot.
// These are diagnostics, not a layout policy or an automatic publication gate.
type reportPreviewUICheck struct {
	Theme              string   `json:"theme"`
	Width              int      `json:"width"`
	ViewportWidth      float64  `json:"viewport_width"`
	FrameWidth         float64  `json:"frame_width"`
	ContentWidth       float64  `json:"content_width"`
	HorizontalOverflow float64  `json:"horizontal_overflow_px"`
	OverflowElements   []string `json:"overflow_elements"`
	MissingStylesheets []string `json:"missing_stylesheets"`
	StylesheetCount    int      `json:"stylesheet_count"`
	BrokenImages       int      `json:"broken_images"`
	UnnamedControls    []string `json:"unnamed_controls"`
	SmallTextCount     int      `json:"small_text_count"`
	ScannedElements    int      `json:"scanned_elements"`
	ScanTruncated      bool     `json:"scan_truncated"`
	Error              string   `json:"error,omitempty"`
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

// Result links use the same public address as dashboard links. The headless
// browser still uses the internal API address; result links carry no token and
// require the recipient's normal sign-in and project access.
func (api *StreamingAPI) reportPreviewResultBaseURL() string {
	base := effectiveShareBaseURL()
	if base == "" {
		base = api.GetCodeExecAPIURL()
	}
	return strings.TrimRight(base, "/")
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
				"description": "Viewport width(s): tablet (768px, primary), mobile (390px), desktop (1280px), or all. Default all. Legacy both means desktop + mobile.",
			},
		},
		"additionalProperties": false,
	}
	description := "Render one HTML document under db/reports/ in a headless browser exactly as the Report tab does and return the real outcome: settled/error state, script errors, failed data reads, tab labels, stale loading text, page height, real viewport screenshots and UI measurements (overflow, missing CSS, broken images and unnamed controls). document_path defaults to db/reports/index.html. Run after dashboard action=validate passes; it is slower (~10-30s) and needs a browser."
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
	if strings.TrimSpace(previewClaims.UserID) == "" {
		return "", fmt.Errorf("preview_report needs an authenticated caller")
	}
	token, err := mintReportPreviewToken(previewClaims, workspacePath)
	if err != nil {
		return "", fmt.Errorf("mint preview token: %w", err)
	}
	resultBase := api.reportPreviewResultBaseURL()
	resultPageURL, err := parseDashboardURL(sharedAssetPublicURL(resultBase, "report", workspacePath), documentPath)
	if err != nil {
		return "", fmt.Errorf("preview link: %w", err)
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
	previewID := uuid.NewString()
	session := "report-preview-" + previewID
	// API/MCP previews have no chat shell config. Give the trusted preview its
	// own guard and browser namespace; borrowing the caller's session would
	// reuse their browser, and slot servers refuse an identity-free command.
	_, screenshotPath := reportPreviewScreenshotPaths(workspacePath, documentPath, themes[0], widths[0])
	screenshotDir := path.Join(workspacePath, path.Dir(screenshotPath), previewID) + "/"
	common.SetSessionWorkingDir(session, workspacePath)
	common.SetSessionFolderGuard(session, []string{workspacePath + "/"}, []string{screenshotDir})
	if parent := common.GetSessionShellConfig(sessionID); parent != nil {
		common.SetSessionFolderGuardBlockedPaths(session, parent.BlockedPaths)
		common.SetSessionFolderGuardBlockedWritePaths(session, parent.BlockedWritePaths)
	}
	common.BindSessionBrowserIsolationForSession(session)
	defer common.ClearSessionShellConfig(session)
	browserCtx := context.WithValue(ctx, common.UserIDKey, previewClaims.UserID)
	browserCtx = context.WithValue(browserCtx, common.ChatSessionIDKey, session)
	browserCtx = context.WithValue(browserCtx, common.WorkflowSessionIDKey, session)
	browserCtx = context.WithValue(browserCtx, common.BrowserDownloadsPathKey, workspacePath)
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
	uiChecks := []reportPreviewUICheck{}
	if snapshot.PreviewState == "ready" || snapshot.PreviewState == "error" {
		for _, theme := range themes {
			if _, err := eval(fmt.Sprintf("window.__reportPreview.setTheme(%q)", theme)); err != nil {
				log.Printf("[REPORT_PREVIEW] set theme %s: %v", theme, err)
			}
			for _, width := range widths {
				px := reportPreviewWidthPixels(width)
				check := reportPreviewUICheck{Theme: theme, Width: px}
				if _, err := run("set", "viewport", fmt.Sprint(px), "900"); err != nil {
					check.Error = "Could not set the browser viewport."
				}
				if _, err := eval(fmt.Sprintf("window.__reportPreview.setWidth(%d)", px)); err != nil {
					log.Printf("[REPORT_PREVIEW] set width %d: %v", px, err)
				}
				time.Sleep(400 * time.Millisecond)
				if raw, err := eval("(async()=>JSON.stringify(await window.__reportPreview.getUIChecks()))()"); err != nil {
					check.Error = "Could not inspect the rendered dashboard."
				} else if err := json.Unmarshal([]byte(raw), &check); err != nil {
					check.Error = "Could not read dashboard UI measurements."
				}
				if check.Error == "" && (check.ViewportWidth != float64(px) || check.FrameWidth <= 0 || check.FrameWidth > float64(px)+1) {
					check.Error = "Could not verify the requested browser viewport and dashboard frame."
				}
				uiChecks = append(uiChecks, check)
				destination, reported := reportPreviewScreenshotPaths(workspacePath, documentPath, theme, width)
				reported = path.Join(path.Dir(reported), previewID, path.Base(reported))
				destination = workspacePath + "/" + reported
				shot := reportPreviewScreenshot{Theme: theme, Width: px, Path: reported, URL: sharedAssetPublicURL(resultBase, "file", path.Join(workspacePath, reported))}
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
	uiIssues := 0
	for _, check := range uiChecks {
		if check.Error != "" || check.HorizontalOverflow > 1 || len(check.MissingStylesheets) > 0 || check.BrokenImages > 0 || len(check.UnnamedControls) > 0 {
			uiIssues++
		}
	}
	if uiIssues > 0 {
		summary += fmt.Sprintf(" UI checks found issues in %d viewport/theme capture(s); inspect ui_checks and screenshots.", uiIssues)
	}
	result := map[string]interface{}{
		"document_path":          documentPath,
		"workspace":              workspacePath,
		"state":                  snapshot.PreviewState,
		"summary":                summary,
		"title":                  snapshot.Report.Title,
		"report_state":           snapshot.Report.State,
		"script_errors":          nonNilStrings(snapshot.Report.Errors),
		"page_errors":            nonNilStrings(snapshot.ConsoleErrors),
		"fetch_errors":           nonNilStrings(snapshot.FetchErrors),
		"tabs":                   nonNilStrings(snapshot.Report.Tabs),
		"loading_texts":          nonNilStrings(snapshot.Report.LoadingTexts),
		"markdown_texts":         nonNilStrings(snapshot.Report.MarkdownTexts),
		"opened_files":           nonNilStrings(snapshot.OpenedFiles),
		"height_px":              snapshot.Report.Height,
		"screenshots":            screenshots,
		"ui_checks":              uiChecks,
		"visual_review_required": true,
		"rendered_via":           "headless browser, same report host runtime as the Report tab",
		"next_step":              "Inspect the screenshots with your client's image viewer, or open their URLs in a browser signed in to this server with current project access. If you cannot view them, say visual inspection is still needed. Inspect desktop/mobile screenshots and ui_checks for overflow, missing styles, broken images and unnamed controls. Small text is a measurement for review, not a design rule. Follow the designer's preferences; fix runtime/data errors and do not claim visual acceptance from ready alone.",
		"page_url":               resultPageURL,
		"authentication":         "Screenshot and page URLs require sign-in and current project access; these URLs grant no access.",
		"settle_timeout":         reportPreviewSettleTimeout.String(),
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
	if len(s.ConsoleErrors) > 0 {
		parts = append(parts, fmt.Sprintf("%d browser error(s)", len(s.ConsoleErrors)))
	}
	failedShots := 0
	for _, shot := range shots {
		if shot.Error != "" {
			failedShots++
		}
	}
	if failedShots > 0 {
		parts = append(parts, fmt.Sprintf("%d screenshot(s) could not be captured", failedShots))
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
		return fmt.Sprintf("Rendered without runtime errors: %d tab label(s), %.0fpx tall, %d screenshot(s). Visual inspection is still required.", len(s.Report.Tabs), s.Report.Height, len(shots))
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
