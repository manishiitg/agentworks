package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGetFileLinkToolCreatesAuthenticatedFileAndFolderLinks(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example/")
	reg := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(reg, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.tools["get_file_link"]
	if !ok || !strings.Contains(tool.desc, "grants no access") {
		t.Fatalf("get_file_link registration = %+v", tool)
	}

	for _, tc := range []struct {
		path string
		kind string
	}{
		{path: "docs/process.md", kind: "file"},
		{path: "docs", kind: "folder"},
	} {
		out, err := tool.exec(context.Background(), map[string]interface{}{"path": tc.path})
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result["kind"] != tc.kind || result["path"] != tc.path {
			t.Fatalf("%s metadata = %#v", tc.path, result)
		}
		preview, err := url.Parse(result["preview_url"].(string))
		if err != nil || preview.Path != "/"+tc.kind {
			t.Fatalf("%s preview = %v err=%v", tc.path, preview, err)
		}
		if result["url"] != result["preview_url"] {
			t.Fatalf("%s primary URL and compatibility alias differ: %#v", tc.path, result)
		}
		decoded, err := base64.StdEncoding.DecodeString(preview.Query().Get("path"))
		if err != nil || string(decoded) != "Workflow/invoices/"+tc.path {
			t.Fatalf("%s encoded path = %q err=%v", tc.path, decoded, err)
		}
		if strings.Contains(preview.RawQuery, "token") {
			t.Fatal("share URL contains a credential")
		}
	}
}

func TestGetReportLinkToolCreatesAuthenticatedDashboardLink(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example/")
	f.write(t, "Workflow/invoices/db/reports/index.html", "<html><title>Invoices</title></html>")
	reg := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(reg, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.tools["get_report_link"]
	if !ok || !strings.Contains(tool.desc, "dashboard") || !strings.Contains(tool.desc, "grants no access") {
		t.Fatalf("get_report_link registration = %+v", tool)
	}
	out, err := tool.exec(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result["kind"] != "report" || result["path"] != "db/reports/index.html" || result["url"] != result["preview_url"] {
		t.Fatalf("report metadata = %#v", result)
	}
	preview, err := url.Parse(result["url"].(string))
	if err != nil || preview.Path != "/report" {
		t.Fatalf("report preview = %v err=%v", preview, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Query().Get("path"))
	if err != nil || string(decoded) != "Workflow/invoices" {
		t.Fatalf("report encoded path = %q err=%v", decoded, err)
	}
	if strings.Contains(preview.RawQuery, "token") {
		t.Fatal("report URL contains a credential")
	}
	if _, err := reg.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": "db/reports/index.html"}); err == nil || !strings.Contains(err.Error(), "use get_report_link") {
		t.Fatalf("report entry accepted by get_file_link: %v", err)
	}
}

func TestGetReportLinkToolTargetsNamedReportDocument(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example")
	f.write(t, "Workflow/invoices/db/reports/tasks.html", "<html><title>Tasks</title></html>")
	reg := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(reg, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	out, err := reg.tools["get_report_link"].exec(context.Background(), map[string]interface{}{"document_path": "db/reports/tasks.html"})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	preview, err := url.Parse(result["url"].(string))
	if err != nil || preview.Query().Get("document") != "db/reports/tasks.html" || result["path"] != "db/reports/tasks.html" {
		t.Fatalf("named report metadata = %#v url=%v err=%v", result, preview, err)
	}
}

func TestGetReportLinkToolRejectsUnauthorizedOrMissingReport(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example")

	denied := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(denied, "outsider", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	if _, err := denied.tools["get_report_link"].exec(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("outsider created a workflow report link")
	}

	allowed := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(allowed, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	if _, err := allowed.tools["get_report_link"].exec(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("missing workflow report received a link")
	}
}

func TestShareLinkToolsMarkLocalhostURLsAsLocalOnly(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "http://localhost:3000")
	f.write(t, "Workflow/invoices/db/reports/index.html", "<html><title>Invoices</title></html>")
	reg := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(reg, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"get_file_link", "get_report_link"} {
		args := map[string]interface{}{}
		if name == "get_file_link" {
			args["path"] = "docs/process.md"
		}
		out, err := reg.tools[name].exec(context.Background(), args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result["shareable"] != false || result["scope"] != "local_machine" || !strings.Contains(result["warning"].(string), "not shareable") {
			t.Fatalf("%s local metadata = %#v", name, result)
		}
	}
}

func TestAddShareabilityMetadataClassifiesDeploymentHosts(t *testing.T) {
	for _, tc := range []struct {
		publicURL string
		shareable bool
		scope     string
	}{
		{publicURL: "http://localhost:3000", shareable: false, scope: "local_machine"},
		{publicURL: "http://app.localhost:3000", shareable: false, scope: "local_machine"},
		{publicURL: "http://127.8.9.10:3000", shareable: false, scope: "local_machine"},
		{publicURL: "http://[::1]:3000", shareable: false, scope: "local_machine"},
		{publicURL: "https://customer.agentworkshq.com", shareable: true, scope: "deployment"},
	} {
		result := map[string]interface{}{}
		addShareabilityMetadata(result, tc.publicURL)
		if result["shareable"] != tc.shareable || result["scope"] != tc.scope {
			t.Fatalf("%s metadata = %#v", tc.publicURL, result)
		}
		_, warned := result["warning"]
		if warned == tc.shareable {
			t.Fatalf("%s warning presence = %t, metadata = %#v", tc.publicURL, warned, result)
		}
	}
}

func TestGetFileLinkToolScopesWorkLinksToProjectOwner(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example")
	const userID = "work-user"
	f.write(t, "_users/"+userID+"/Chats/Work/projects/demo/output/report.txt", "ready")
	reg := &recordingRegistrar{}
	if err := f.api.registerWorkShareLinkTool(reg, userID, "Chats/Work/projects/demo"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		kind string
	}{
		{path: "output/report.txt", kind: "file"},
		{path: "output", kind: "folder"},
	} {
		out, err := reg.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": tc.path})
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		preview, err := url.Parse(result["preview_url"].(string))
		if err != nil || preview.Path != "/"+tc.kind || preview.Query().Get("uid") != userID {
			t.Fatalf("Work preview = %v err=%v", preview, err)
		}
		if result["url"] != result["preview_url"] {
			t.Fatalf("Work primary URL and compatibility alias differ: %#v", result)
		}
		decoded, err := base64.StdEncoding.DecodeString(preview.Query().Get("path"))
		if err != nil || string(decoded) != "Chats/Work/projects/demo/"+tc.path {
			t.Fatalf("Work encoded path = %q err=%v", decoded, err)
		}
		if tc.kind == "file" {
			request := adminRequest("GET", preview.RequestURI(), "", &UserClaims{UserID: userID}, nil)
			response := httptest.NewRecorder()
			f.api.handlePublicFile(response, request)
			if response.Code != 200 || response.Body.String() != "ready" {
				t.Fatalf("owner could not open Work link: %d %s", response.Code, response.Body.String())
			}
			request = adminRequest("GET", preview.RequestURI(), "", &UserClaims{UserID: "other-user"}, nil)
			response = httptest.NewRecorder()
			f.api.handlePublicFile(response, request)
			if response.Code != 403 {
				t.Fatalf("another user opened a personal Work link: %d", response.Code)
			}
		}
	}

	if _, err := reg.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": "../outside.txt"}); err == nil {
		t.Fatal("Work share tool accepted traversal")
	}
	if err := f.api.registerWorkShareLinkTool(&recordingRegistrar{}, userID, "Chats/another-project"); err == nil {
		t.Fatal("Work share tool accepted a non-Work project root")
	}

	// Resumed Work conversations carry the expanded private path. It must
	// register the same tool and still emit the canonical user-scoped URL.
	physical := &recordingRegistrar{}
	if err := f.api.registerWorkShareLinkTool(physical, userID, "_users/work-user/Chats/Work/projects/demo"); err != nil {
		t.Fatalf("register share tool for resumed Work path: %v", err)
	}
	out, err := physical.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": "output/report.txt"})
	if err != nil {
		t.Fatalf("create link from resumed Work path: %v", err)
	}
	var resumedResult map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resumedResult); err != nil {
		t.Fatal(err)
	}
	preview, err := url.Parse(resumedResult["preview_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.Query().Get("path"))
	if err != nil || string(decoded) != "Chats/Work/projects/demo/output/report.txt" {
		t.Fatalf("resumed Work encoded path = %q err=%v", decoded, err)
	}
}

func TestGetReportLinkToolScopesWorkDashboardToProjectOwner(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example")
	const userID = "work-user"
	f.write(t, "_users/"+userID+"/Chats/Work/projects/demo/db/reports/index.html", "<html><title>Work dashboard</title></html>")

	for _, workspace := range []string{
		"Chats/Work/projects/demo",
		"_users/work-user/Chats/Work/projects/demo",
	} {
		reg := &recordingRegistrar{}
		if err := f.api.registerWorkShareLinkTool(reg, userID, workspace); err != nil {
			t.Fatalf("register Work report tool for %s: %v", workspace, err)
		}
		out, err := reg.tools["get_report_link"].exec(context.Background(), map[string]interface{}{})
		if err != nil {
			t.Fatalf("create Work report link for %s: %v", workspace, err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		// A report link names the project's real path and carries no uid: recipients
		// open it with their own access (PLAT-750/751).
		preview, err := url.Parse(result["url"].(string))
		if err != nil || preview.Path != "/report" || preview.Query().Get("uid") != "" {
			t.Fatalf("Work report preview = %v err=%v", preview, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(preview.Query().Get("path"))
		if err != nil || string(decoded) != "_users/"+userID+"/Chats/Work/projects/demo" {
			t.Fatalf("Work report encoded path = %q err=%v", decoded, err)
		}
		if strings.Contains(preview.RawQuery, "token") {
			t.Fatal("Work report URL contains a credential")
		}
		if _, err := reg.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": "db/reports/index.html"}); err == nil || !strings.Contains(err.Error(), "use get_report_link") {
			t.Fatalf("Work report entry accepted by get_file_link: %v", err)
		}
	}
}

func TestGetFileLinkToolRejectsUnauthorizedPrivateAndMissingPaths(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("PUBLIC_URL", "https://customer.example")

	denied := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(denied, "outsider", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	if _, err := denied.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": "docs/process.md"}); err == nil {
		t.Fatal("outsider created a workflow link")
	}

	allowed := &recordingRegistrar{}
	if err := f.api.registerShareLinkTools(allowed, "owner", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../secret", "Workflow/invoices/docs/process.md", "secrets/token", "docs/missing.pdf", "."} {
		if _, err := allowed.tools["get_file_link"].exec(context.Background(), map[string]interface{}{"path": p}); err == nil {
			t.Fatalf("unsafe or missing path accepted: %s", p)
		}
	}
}
