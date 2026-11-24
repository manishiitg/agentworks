package claudecode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3189: an attached PDF reaches Claude next to the text, as the bridge's document input; an unsupported type is
// still skipped and keeps its file path.
func TestClaudeSDKSendsAttachedPDF(t *testing.T) {
	owner := "pdf-chat"
	closed := 0
	pdf := "%PDF-1.7 example"
	skipped := ""
	inputs := []llmtypes.NativeImageInput{
		{ID: "pdf", Open: func(context.Context) (io.ReadCloser, string, int64, error) {
			return &sdkImageReader{strings.NewReader(pdf), &closed}, "application/pdf", int64(len(pdf)), nil
		}},
		{ID: "zip", Skipped: func(reason string) { skipped = reason }, Open: func(context.Context) (io.ReadCloser, string, int64, error) {
			return &sdkImageReader{strings.NewReader("zip"), &closed}, "application/zip", 3, nil
		}},
	}
	ctx := llmtypes.WithNativeImageInputs(context.Background(), owner, inputs)
	params, done, err := claudeSDKParamsWithImages(ctx, owner, map[string]any{"type": "prompt", "text": "Summarise this"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	streaming, ok := params.(claudeSDKStreamingParams)
	if !ok {
		t.Fatalf("PDF was not sent natively: %#v", params)
	}
	var out bytes.Buffer
	if err := streaming.writeJSON(&out); err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Text   string `json:"text"`
		Images []struct {
			MediaType string `json:"mediaType"`
			Data      string `json:"data"`
		} `json:"images"`
	}
	if err := json.Unmarshal(out.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Text != "Summarise this" || len(frame.Images) != 1 || frame.Images[0].MediaType != "application/pdf" || frame.Images[0].Data != base64.StdEncoding.EncodeToString([]byte(pdf)) {
		t.Fatalf("frame=%+v", frame)
	}
	if skipped != "image_limit" {
		t.Fatalf("unsupported type not skipped: %q", skipped)
	}
}
