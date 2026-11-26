package paneview

import (
	"strings"
	"testing"
)

func TestCollapseBlankRuns(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "no blanks", in: "a\nb\nc", want: "a\nb\nc"},
		{name: "single blank preserved", in: "a\n\nb", want: "a\n\nb"},
		{name: "double blank preserved", in: "a\n\n\nb", want: "a\n\n\nb"},
		{name: "triple blank collapsed to two", in: "a\n\n\n\nb", want: "a\n\n\nb"},
		{name: "huge blank run collapsed to two", in: "a\n\n\n\n\n\n\n\nb", want: "a\n\n\nb"},
		{name: "trailing whitespace trimmed per line", in: "a   \nb\t\n  c", want: "a\nb\n  c"},
		{name: "whitespace-only lines treated as blank",
			in:   "a\n   \n\t\n  \nb",
			want: "a\n\n\nb"},
		{name: "spinner-frame pattern from tmux capture (stale pruned, active kept)",
			in:   "⣟ Generating...\n\n\n\n\n\n\n\n\n\n\n⣽ Generating.\n",
			want: "⣽ Generating.\n"},
		{name: "stale spinner pattern (pruned when followed by non-blank lines)",
			in:   "⣟ Generating...\n\n\n● Done\n\n",
			want: "● Done\n\n"},
		{name: "multiple stale spinners and one active spinner",
			in:   "⣟ Generating...\n⣽ Generating.\n● Step 1\n⣻ Generating..\n\n",
			want: "● Step 1\n⣻ Generating..\n\n"},
		// Real garbled capture: in-place spinner redraw flattened into staggered
		// word fragments ("oading"/"king.."/"enerat"/"Worki"/"nerati") with NO
		// leading Braille glyph — the glyph landed on a different column than the
		// text. A whole screen of these fragment-runs must collapse away, leaving
		// only genuine content.
		{name: "staggered spinner-word fragments (no braille) pruned",
			in:   "or\n..\noading\nng\nner\nking..\nenerat\nin\nting..\ning...\nen\nng.\nWorki\nrking.\nng\ng.\nor\n..\nnerati\nGenerating...",
			want: ""},
		{name: "spinner-word fragments around real content keep the content",
			in:   "Here is the real answer line.\noading\nking..\nWorki\nrking.\nGenerating...\nAnother real line.",
			want: "Here is the real answer line.\nAnother real line."},
		{name: "trailing spinner cell on content line stripped",
			in:   "Final answer text    ⠧ Working...\nNext line",
			want: "Final answer text\nNext line"},
		{name: "isolated short word that is not a fragment-run is kept",
			in:   "result or error\nor\nfinal text",
			want: "result or error\nor\nfinal text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CollapseBlankRuns(tc.in); got != tc.want {
				t.Fatalf("CollapseBlankRuns(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripANSIPreserveColors(t *testing.T) {
	esc := "\x1b"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text untouched", in: "hello world", want: "hello world"},
		{
			name: "SGR color preserved",
			in:   esc + "[31mRED" + esc + "[0m",
			want: esc + "[31mRED" + esc + "[0m",
		},
		{
			name: "bold preserved",
			in:   esc + "[1mbold" + esc + "[22m",
			want: esc + "[1mbold" + esc + "[22m",
		},
		{
			name: "cursor-position stripped (ends in H)",
			in:   esc + "[2;5Hmoved",
			want: "moved",
		},
		{
			name: "clear-screen stripped (ends in J)",
			in:   esc + "[2Jcleared",
			want: "cleared",
		},
		{
			name: "mixed: keep color, drop cursor move",
			in:   esc + "[31m" + esc + "[5;1Hred-after-move" + esc + "[0m",
			want: esc + "[31m" + "red-after-move" + esc + "[0m",
		},
		{
			name: "incomplete trailing escape dropped",
			in:   "visible" + esc + "[38;5;",
			want: "visible",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripANSIPreserveColors(tt.in); got != tt.want {
				t.Errorf("StripANSIPreserveColors(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A tmux-backed CLI adapter historically broke by surfacing the "Generating…"
// spinner frame as the response. Spinner pruning runs inside CollapseBlankRuns (which
// StripANSIPreserveColors calls), so a scrolled-up spinner frame followed by
// real content must not survive into the displayed snapshot. This locks that
// the shared strip path keeps that behavior.
func TestStripANSIPreserveColorsPrunesScrolledSpinner(t *testing.T) {
	esc := "\x1b"
	// A historical spinner frame (Braille glyph) colored via SGR, followed by
	// real assistant content — the spinner frame should be pruned.
	in := esc + "[2m⣾ Generating…" + esc + "[0m\nActual answer here"
	got := StripANSIPreserveColors(in)
	if !strings.Contains(got, "Actual answer here") {
		t.Fatalf("real content lost: %q", got)
	}
	if strings.Contains(got, "Generating") {
		t.Errorf("scrolled-up spinner frame leaked into snapshot: %q", got)
	}
}
