package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// registerShareLinkTools exposes the same authenticated preview links as the
// workspace UI. A link identifies an artifact; it never grants access.
func (api *StreamingAPI) registerShareLinkTools(reg definitionToolRegistrar, userID, workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	if err := reg.RegisterCustomTool("get_file_link", "Create an authenticated AgentWorks preview link for an existing non-dashboard file or folder in the active workflow. Pass a canonical workflow-relative path such as runs/latest/report.pdf or runs/latest; the server validates current workflow access, existence, protected-path rules, and whether the target is a file or folder. A request for db/reports/index.html is rejected because dashboards must use get_report_link and the dedicated report runtime. Present the returned url value verbatim; never manually build, rewrite, or Base64-encode a /file or /folder URL. Inspect shareable and warning in the result: when PUBLIC_URL is localhost or another loopback host, the URL is a same-machine preview only and must not be described as shareable. The URL contains no credential and grants no access: every recipient must sign in and already have access to this workflow. This is not publishing and cannot create anonymous links or share arbitrary web URLs.", map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"path"},
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "Canonical path relative to the active workflow root. Do not prefix it with Workflow/<id>."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID})
		if userAccessForClaims(&UserClaims{UserID: userID}).Disabled {
			return "", fmt.Errorf("access denied: disabled account")
		}
		if _, err := authorizeWorkflowContextPaths(ctx, []string{workspace}); err != nil {
			return "", fmt.Errorf("workflow is unavailable or access denied")
		}

		raw, _ := args["path"].(string)
		relative := strings.TrimSpace(raw)
		clean, err := wf.CleanRelative(relative)
		if err != nil || clean == "." || clean != relative || strings.HasPrefix(clean, "Workflow/") {
			return "", fmt.Errorf("path must be a canonical path relative to the active workflow")
		}
		// A dashboard is under db/, which is private to files; say where its link comes from.
		if strings.HasPrefix(clean, "db/reports/") && strings.HasSuffix(strings.ToLower(clean), ".html") {
			return "", fmt.Errorf("%s is a live dashboard; use get_report_link so it opens in the dedicated report runtime", clean)
		}
		if wf.Private(clean) {
			return "", fmt.Errorf("private workspace files are not shareable")
		}

		return createSecureShareLink(ctx, workspace, workspace, clean, "", "Recipient must sign in to AgentWorks and already have access to this workflow. The link contains no credential and grants no access.")
	}, "workflow_files"); err != nil {
		return err
	}

	return reg.RegisterCustomTool("get_report_link", "Create an authenticated AgentWorks dashboard link for an HTML document under db/reports/. document_path defaults to db/reports/index.html. The server validates access and confirms that the document exists. Present the returned url verbatim. This is secure internal sharing, not anonymous publishing: the link contains no credential and grants no access, so recipients must already have workflow access.", map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"document_path": map[string]interface{}{"type": "string", "description": "Report HTML path under db/reports/. Defaults to db/reports/index.html."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID})
		if userAccessForClaims(&UserClaims{UserID: userID}).Disabled {
			return "", fmt.Errorf("access denied: disabled account")
		}
		if _, err := authorizeWorkflowContextPaths(ctx, []string{workspace}); err != nil {
			return "", fmt.Errorf("workflow is unavailable or access denied")
		}
		reportPath, err := cleanReportLinkPath(args["document_path"])
		if err != nil {
			return "", err
		}
		return createSecureReportLink(ctx, workspace, workspace, reportPath, "", "Recipient must sign in to AgentWorks and already have access to this workflow. The link contains no credential and grants no access.")
	}, "workflow_files")
}

// registerWorkShareLinkTool exposes share links for the active Work project.
// uid names the crew owner so other signed-in users with Crew access resolve
// the owner's tree (see shared_assets_crew.go); it is not a credential.
//
// A Code link resolves through the ordinary personal-file rules, which admit
// only the owner: crewReaderSharedAsset opens other owners' Crews, never Codes.
func (api *StreamingAPI) registerWorkShareLinkTool(reg definitionToolRegistrar, userID, workspace string) error {
	cleanWorkspace, err := cleanAgentProfileWorkspace(workspace, userID)
	if err != nil || cleanWorkspace != workspace || !isActiveWorkProjectWorkspace(userID, cleanWorkspace) {
		return fmt.Errorf("share links require an active project")
	}
	// The product name comes from the product's own product.yaml
	// (profile.name), so tool text says Crew or Code without hardcoding it.
	name := api.projectProductName(userID, cleanWorkspace)
	noun := name + " project"
	if isCodeProjectPath(cleanWorkspace) {
		return registerProjectShareLinkTools(reg, userID, cleanWorkspace, noun,
			"Only you (the owner of this "+noun+") can open it after signing in; it is private to you.",
			"Only you (the owner of this "+noun+") can open this dashboard after signing in.")
	}
	return registerProjectShareLinkTools(reg, userID, cleanWorkspace, noun,
		"Recipients must sign in to AgentWorks and have access to this "+noun+" (the owner, or anyone with "+name+" access), and they get read-only access to that file or folder only.",
		"Recipients must sign in and have current "+name+" access. Dashboard scripts use the viewer's permissions for selected data sources.")
}

// registerProjectShareLinkTools registers get_file_link and get_report_link
// for one project; noun names the project in tool text, and the two access
// sentences say who can open each kind of link.
func registerProjectShareLinkTools(reg definitionToolRegistrar, userID, cleanWorkspace, noun, fileAccess, reportAccess string) error {
	canonicalWorkspace := canonicalChatHistoryWorkspacePath(userID, cleanWorkspace)
	physicalRoot := agentProfileRuntimeWorkspace(userID, canonicalWorkspace)
	if err := reg.RegisterCustomTool("get_file_link", "Create an authenticated preview link for an existing file or folder in the active "+noun+". Pass a canonical project-relative path; the server validates existence, protected-path rules, and whether the target is a file or folder. Present the returned url value verbatim; never manually build, rewrite, or Base64-encode a /file or /folder URL. Inspect shareable and warning in the result: when PUBLIC_URL is localhost or another loopback host, the URL is a same-machine preview only and must not be described as shareable. The URL contains no credential. "+fileAccess+" The builder/ transcripts, db/ databases and root product.json/workflow.json stay private to the owner even when linked. This is not public publishing and cannot share arbitrary web URLs or files outside this project.", map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"path"},
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "Canonical path relative to the active " + noun + "."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		raw, _ := args["path"].(string)
		relative := strings.TrimSpace(raw)
		clean, err := wf.CleanRelative(relative)
		if err != nil || clean == "." || clean != relative {
			return "", fmt.Errorf("path must be a canonical path relative to the active %s", noun)
		}
		// A dashboard is under db/, which is private to files; say where its link comes from.
		if strings.HasPrefix(clean, "db/reports/") && strings.HasSuffix(strings.ToLower(clean), ".html") {
			return "", fmt.Errorf("%s is a live dashboard; use get_report_link so it opens in the dedicated report runtime", clean)
		}
		if wf.Private(clean) {
			return "", fmt.Errorf("private workspace files are not shareable")
		}
		return createSecureShareLink(ctx, physicalRoot, canonicalWorkspace, clean, userID, fileAccess+" The link contains no credential.")
	}, "work_files"); err != nil {
		return err
	}

	return reg.RegisterCustomTool("get_report_link", "Create an authenticated dashboard link for an HTML document under the "+noun+"'s db/reports/. document_path defaults to db/reports/index.html. The link contains no credential. "+reportAccess, map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"document_path": map[string]interface{}{"type": "string", "description": "Report HTML path under db/reports/. Defaults to db/reports/index.html."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		reportPath, err := cleanReportLinkPath(args["document_path"])
		if err != nil {
			return "", err
		}
		return createSecureReportLink(ctx, physicalRoot, physicalRoot, reportPath, "", reportAccess+" The link contains no credential.")
	}, "work_files")
}

func cleanReportLinkPath(value interface{}) (string, error) {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return "db/reports/index.html", nil
	}
	clean, err := wf.CleanRelative(raw)
	if err != nil || clean != raw || !strings.HasPrefix(clean, "db/reports/") || !strings.HasSuffix(strings.ToLower(clean), ".html") {
		return "", fmt.Errorf("document_path must be a canonical .html path under db/reports/")
	}
	return clean, nil
}

func createSecureReportLink(ctx context.Context, metadataRoot, linkRoot, reportPath, userID, authentication string) (string, error) {
	// db/ is a private path for the asset service, so a report is checked
	// through the dashboards service that owns it.
	if err := reportDocumentExists(ctx, metadataRoot, reportPath); err != nil {
		return "", fmt.Errorf("project report is unavailable: %w", err)
	}
	publicURL := effectiveShareBaseURL()
	if publicURL == "" {
		return "", fmt.Errorf("PUBLIC_URL is not configured on this server; cannot create a report link")
	}
	previewURL := sharedAssetPublicURLForUser(publicURL, "report", linkRoot, userID)
	if reportPath != "db/reports/index.html" {
		parsedPreview, err := url.Parse(previewURL)
		if err != nil {
			return "", fmt.Errorf("cannot create report URL: %w", err)
		}
		query := parsedPreview.Query()
		query.Set("document", reportPath)
		parsedPreview.RawQuery = query.Encode()
		previewURL = parsedPreview.String()
	}
	result := map[string]interface{}{
		"kind":           "report",
		"path":           reportPath,
		"url":            previewURL,
		"preview_url":    previewURL,
		"authentication": authentication,
	}
	addShareabilityMetadata(result, publicURL)
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("cannot encode report link metadata: %w", err)
	}
	return string(encoded), nil
}

// reportDocumentExists confirms a dashboard document under db/reports/: a
// managed revision resolves through its state, a plain HTML dashboard must be
// in the project's dashboard list.
func reportDocumentExists(ctx context.Context, root, reportPath string) error {
	if strings.HasPrefix(reportPath, dashboards.Prefix) {
		_, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "resolve", DocumentPath: reportPath})
		return err
	}
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "list"})
	if err != nil {
		return err
	}
	for _, dashboard := range listed.Dashboards {
		if dashboard.DocumentPath == reportPath {
			return nil
		}
	}
	return fmt.Errorf("no dashboard at %s", reportPath)
}

func createSecureShareLink(ctx context.Context, metadataRoot, linkRoot, relative, userID, authentication string) (string, error) {
	metadata, err := sharedAssetMetadata(ctx, metadataRoot, relative)
	if err != nil {
		return "", err
	}
	kind, _ := metadata["type"].(string)
	if kind != "file" && kind != "folder" {
		return "", fmt.Errorf("asset service returned an unsupported target type")
	}
	publicURL := effectiveShareBaseURL()
	if publicURL == "" {
		return "", fmt.Errorf("PUBLIC_URL is not configured on this server; cannot create a share link")
	}
	metadata["path"] = relative
	metadata["kind"] = kind
	previewURL := sharedAssetPublicURLForUser(publicURL, kind, path.Join(linkRoot, relative), userID)
	metadata["url"] = previewURL
	metadata["preview_url"] = previewURL
	metadata["authentication"] = authentication
	addShareabilityMetadata(metadata, publicURL)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("cannot encode share link metadata: %w", err)
	}
	return string(encoded), nil
}

func addShareabilityMetadata(result map[string]interface{}, publicURL string) {
	parsed, err := url.Parse(publicURL)
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	ip := net.ParseIP(hostname)
	localOnly := err != nil || hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || (ip != nil && ip.IsLoopback())
	result["shareable"] = !localOnly
	if localOnly {
		result["scope"] = "local_machine"
		result["warning"] = "PUBLIC_URL uses localhost or a loopback address. This URL works only from the machine running AgentWorks and is not shareable with another user or device. Configure a reachable deployment PUBLIC_URL before offering a shareable link."
		return
	}
	result["scope"] = "deployment"
}

func sharedAssetMetadata(ctx context.Context, root, relative string) (map[string]any, error) {
	body, err := json.Marshal(map[string]string{"root": root, "path": relative, "operation": "stat"})
	if err != nil {
		return nil, err
	}
	req, err := newSharedAssetMetadataRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	response, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asset service unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("asset is unavailable or not shareable (status %d)", response.StatusCode)
	}
	var metadata map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("asset service returned invalid metadata")
	}
	return metadata, nil
}

func newSharedAssetMetadataRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", getWorkspaceAPIURL()+"/api/shared-assets", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	req.Header.Set("X-Asset-Method", "POST")
	return req, nil
}

func sharedAssetPublicURL(baseURL, kind, full string) string {
	return sharedAssetPublicURLForUser(baseURL, kind, full, "")
}

func sharedAssetPublicURLForUser(baseURL, kind, full, userID string) string {
	q := url.Values{"path": {base64.StdEncoding.EncodeToString([]byte(full))}}
	if strings.TrimSpace(userID) != "" {
		q.Set("uid", userID)
	}
	return strings.TrimRight(baseURL, "/") + "/" + kind + "?" + q.Encode()
}
