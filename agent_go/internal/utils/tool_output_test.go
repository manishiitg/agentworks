package utils

import "testing"

func TestExtractActualContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "json text wrapper with content",
			content: `{"type":"text","text":"hello world"}`,
			want:    "hello world",
		},
		{
			name:    "json text wrapper with empty string",
			content: `{"type":"text","text":""}`,
			want:    "",
		},
		{
			name:    "json text wrapper with escaped quotes",
			content: `{"type":"text","text":"say \"hi\""}`,
			want:    `say "hi"`,
		},
		{
			name:    "json text wrapper with escaped newline",
			content: `{"type":"text","text":"line1\nline2"}`,
			want:    "line1\nline2",
		},
		{
			name:    "json text wrapper with escaped tab",
			content: `{"type":"text","text":"col1\tcol2"}`,
			want:    "col1\tcol2",
		},
		{
			name:    "tool result prefix",
			content: "TOOL RESULT for tool-123: actual output here",
			want:    "actual output here",
		},
		{
			name:    "plain content without wrapper",
			content: "just some text",
			want:    "just some text",
		},
		{
			name:    "empty string",
			content: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractActualContent(tt.content)
			if got != tt.want {
				t.Errorf("ExtractActualContent(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
