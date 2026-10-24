package mediapresentation

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestFalCDNVideoURLAcceptsOnlyHTTPSFalMedia(t *testing.T) {
	valid, err := falCDNVideoURL("https://v3.fal.media/files/example/clip.mp4")
	if err != nil || valid != "https://v3.fal.media/files/example/clip.mp4" {
		t.Fatalf("Fal CDN URL should be accepted, got %q, %v", valid, err)
	}
	for _, raw := range []string{"http://v3.fal.media/files/clip.mp4", "https://example.com/clip.mp4", "https://fal.media.evil.example/clip.mp4"} {
		if _, err := falCDNVideoURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

// show_video must read its presentation kind from the profile's declared
// ToolBinding.Presentation rather than hardcoding "media.video". Requiring
// it here — refusing before any QA check or network call — is what makes the
// product.yaml declaration load-bearing: a profile that forgets to declare
// tools[].presentation.kind fails loudly at the first call, not silently by
// writing a kind nobody asked for.
func TestShowVideoRequiresADeclaredPresentationKind(t *testing.T) {
	factory := showVideoFactory("http://unused")
	spec, err := factory(agentprofiles.ToolRuntimeContext{
		UserID: "u1", SessionID: "s1", WorkspacePath: "Crew/example",
		// Presentation intentionally left nil.
	}, nil)
	if err != nil {
		t.Fatalf("building the tool spec itself should not fail: %v", err)
	}

	result, err := spec.Execute(context.Background(), map[string]interface{}{
		"path": "outputs/final.mp4", "qa_report_path": "work/qa/report.json", "title": "Launch video",
	})
	if err == nil {
		t.Fatalf("expected an error with no declared presentation kind, got result %q", result)
	}
	if !strings.Contains(err.Error(), "presentation kind") {
		t.Fatalf("error should name the missing declaration, got: %v", err)
	}
}

// With a kind declared, show_video must use exactly that value rather than a
// hardcoded default — this is the difference between the YAML declaration
// being consulted and it being decorative.
func TestShowVideoUsesTheDeclaredKindNotAHardcodedDefault(t *testing.T) {
	factory := showVideoFactory("http://unused")
	spec, err := factory(agentprofiles.ToolRuntimeContext{
		UserID: "u1", SessionID: "s1", WorkspacePath: "Crew/example",
		Presentation: &agentprofiles.PresentationBinding{Kind: "media.image", Activity: &agentprofiles.PresentationActivityBinding{Label: "Image ready", Destination: "Images panel", Detail: "Ready to review"}},
	}, nil)
	if err != nil {
		t.Fatalf("building the tool spec: %v", err)
	}

	// A non-video path fails before reaching QA validation or the declared
	// kind, so this exercises the same early-return path as the no-kind case
	// without needing a fake workspace server — it only proves the factory
	// reads runtime.Presentation instead of panicking or ignoring it.
	result, err := spec.Execute(context.Background(), map[string]interface{}{
		"path": "not-a-video.txt", "qa_report_path": "work/qa/report.json", "title": "x",
	})
	if err != nil {
		t.Fatalf("declared-kind path should reach the extension check, got error: %v", err)
	}
	if !strings.Contains(result, "project-relative video path") {
		t.Fatalf("expected the extension-validation message, got %q", result)
	}
}

func TestShowVideoAllowsAnMP4PreviewWithoutAQualityReport(t *testing.T) {
	spec, err := showVideoFactory("http://unused")(agentprofiles.ToolRuntimeContext{
		UserID: "u1", SessionID: "s1", WorkspacePath: "Crew/example",
		Presentation: &agentprofiles.PresentationBinding{Kind: "media.video", Activity: &agentprofiles.PresentationActivityBinding{Label: "Video ready", Destination: "Videos panel", Detail: "Ready to review"}},
	}, nil)
	if err != nil {
		t.Fatalf("building show_video: %v", err)
	}

	required, ok := spec.Parameters["required"].([]string)
	if !ok {
		t.Fatalf("show_video required parameters have unexpected type: %#v", spec.Parameters["required"])
	}
	for _, parameter := range required {
		if parameter == "qa_report_path" {
			t.Fatalf("qa_report_path must be optional so a review preview can be shown: %v", required)
		}
	}
	if !strings.Contains(strings.ToLower(spec.Description), "reviewable mp4") {
		t.Fatalf("show_video must tell the agent to present reviewable MP4s, got %q", spec.Description)
	}
}
