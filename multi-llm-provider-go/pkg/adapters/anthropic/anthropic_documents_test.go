package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCreateDocumentBlockBase64PDF verifies the adapter constructs a
// valid base64 document block for application/pdf input. The MarshalJSON
// path is exercised so we catch any mis-tagged union variant before it
// reaches the wire.
func TestCreateDocumentBlockBase64PDF(t *testing.T) {
	doc := llmtypes.DocumentContent{
		SourceType: "base64",
		MediaType:  "application/pdf",
		Data:       "JVBERi0xLjQK", // "%PDF-1.4" base64, just enough to look real
		Title:      "test-doc",
	}
	block := createDocumentBlock(doc)
	if block == nil {
		t.Fatal("createDocumentBlock returned nil for valid PDF")
	}
	if block.OfDocument == nil {
		t.Fatal("returned block has nil OfDocument variant")
	}
	if block.OfDocument.Source.OfBase64 == nil {
		t.Fatal("document source variant should be base64 PDF")
	}
	raw, err := json.Marshal(*block)
	if err != nil {
		t.Fatalf("marshal block: %v", err)
	}
	encoded := string(raw)
	if !strings.Contains(encoded, `"type":"document"`) {
		t.Errorf("marshaled block missing type=document: %s", encoded)
	}
	if !strings.Contains(encoded, `"media_type":"application/pdf"`) {
		t.Errorf("marshaled block missing media_type: %s", encoded)
	}
	if !strings.Contains(encoded, `"data":"JVBERi0xLjQK"`) {
		t.Errorf("marshaled block missing data: %s", encoded)
	}
	if !strings.Contains(encoded, `"title":"test-doc"`) {
		t.Errorf("marshaled block missing title: %s", encoded)
	}
}

// TestCreateDocumentBlockURLPDF: URL source uses the URL pdf variant.
func TestCreateDocumentBlockURLPDF(t *testing.T) {
	doc := llmtypes.DocumentContent{
		SourceType: "url",
		MediaType:  "application/pdf",
		Data:       "https://example.com/test.pdf",
	}
	block := createDocumentBlock(doc)
	if block == nil || block.OfDocument == nil {
		t.Fatal("URL-source PDF block should not be nil")
	}
	if block.OfDocument.Source.OfURL == nil {
		t.Fatal("URL-source document should use OfURL variant")
	}
}

// TestConvertMessagesEmitsDocumentBlockOnHumanMessage proves that a
// llmtypes.MessageContent carrying a DocumentContent part is forwarded
// to the Anthropic params as a document content block on the user
// message. This is the regression test for the symptom "model says
// 'I don't see a PDF attached'" — that error means the document part
// was dropped before the API call.
func TestConvertMessagesEmitsDocumentBlockOnHumanMessage(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.DocumentContent{
					SourceType: "base64",
					MediaType:  "application/pdf",
					Data:       "JVBERi0xLjQK",
					Title:      "x",
				},
				llmtypes.TextContent{Text: "What's in the PDF?"},
			},
		},
	}
	anthropicMessages, _ := convertMessages(messages)
	if len(anthropicMessages) != 1 {
		t.Fatalf("expected 1 anthropic message, got %d", len(anthropicMessages))
	}
	msg := anthropicMessages[0]
	foundDoc := false
	for _, b := range msg.Content {
		if b.OfDocument != nil {
			foundDoc = true
			break
		}
	}
	if !foundDoc {
		t.Fatalf("converted user message has no document block; parts: %d, content blocks: %d", len(messages[0].Parts), len(msg.Content))
	}
}

// TestBuildToolResultBlockTextOnly: when no image attachments are
// present, the helper falls back to the SDK's text-only convenience
// constructor so the wire format matches the pre-vision-in-tool-output
// path exactly.
func TestBuildToolResultBlockTextOnly(t *testing.T) {
	block := buildToolResultBlock("call-1", "result text", false, nil)
	if block.OfToolResult == nil {
		t.Fatal("expected tool_result variant")
	}
	if block.OfToolResult.ToolUseID != "call-1" {
		t.Errorf("unexpected ToolUseID: %s", block.OfToolResult.ToolUseID)
	}
}

// TestBuildToolResultBlockWithImage: when images are attached, the
// helper builds a ToolResultBlockParam by hand and includes one image
// content union per attachment. This is the regression test for the
// vision-in-tool-output feature (Anthropic Claude 3.5+).
func TestBuildToolResultBlockWithImage(t *testing.T) {
	imgs := []llmtypes.ImageContent{
		{SourceType: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="},
	}
	block := buildToolResultBlock("call-2", "Found a chart:", false, imgs)
	if block.OfToolResult == nil {
		t.Fatal("expected tool_result variant")
	}
	if block.OfToolResult.ToolUseID != "call-2" {
		t.Errorf("unexpected ToolUseID: %s", block.OfToolResult.ToolUseID)
	}
	// Two content entries expected: text + image.
	if len(block.OfToolResult.Content) != 2 {
		t.Fatalf("expected 2 tool_result content entries (text + image), got %d", len(block.OfToolResult.Content))
	}
	textEntry := block.OfToolResult.Content[0]
	if textEntry.OfText == nil || textEntry.OfText.Text != "Found a chart:" {
		t.Errorf("first content entry should be the text; got %+v", textEntry)
	}
	imgEntry := block.OfToolResult.Content[1]
	if imgEntry.OfImage == nil {
		t.Fatalf("second content entry should be image; got %+v", imgEntry)
	}
}

// TestBuildToolResultBlockMarksErrors: forwarded IsError must round-trip.
func TestBuildToolResultBlockMarksErrors(t *testing.T) {
	block := buildToolResultBlock("call-3", "boom", true, nil)
	if block.OfToolResult == nil {
		t.Fatal("expected tool_result variant")
	}
	raw, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"is_error":true`) {
		t.Errorf("error flag not propagated to wire format: %s", string(raw))
	}
}

// TestCreateDocumentBlockRejectsUnsupportedMediaType: anything that is
// not application/pdf returns nil so the caller can degrade rather than
// shipping a malformed block. Anthropic supports PlainText/ContentBlock
// document variants too but we haven't promoted them to llmtypes yet.
func TestCreateDocumentBlockRejectsUnsupportedMediaType(t *testing.T) {
	cases := []llmtypes.DocumentContent{
		// text/html, text/markdown, etc. are NOT in the supported set
		// (Anthropic only exposes PDF + plain text source variants
		// today). They must be dropped, not silently re-routed.
		{SourceType: "url", MediaType: "text/html", Data: "https://example.com/x.html"},
		{SourceType: "base64", MediaType: "text/markdown", Data: "aGVsbG8="},
		{SourceType: "base64", MediaType: "", Data: "x"},                  // missing media type
		{SourceType: "url", MediaType: "application/pdf", Data: ""},       // empty data
		{SourceType: "base64", MediaType: "application/pdf", Data: ""},    // empty data
		{SourceType: "base64", MediaType: "text/plain", Data: ""},         // empty data
	}
	for i, doc := range cases {
		if createDocumentBlock(doc) != nil {
			t.Errorf("case %d (%+v): expected nil, got block", i, doc)
		}
	}
}
