package server

import (
	"html"
	"strings"
)

// ownerEmailLine is one status line in an owner email (a run in the digest).
type ownerEmailLine struct {
	status, text string
}

// ownerEmailHTML renders the platform's own owner emails (run summary, daily
// digest, work-stopped alert) as one inline-styled HTML body, so they read like
// a short report in the inbox instead of plain text (owner, 2026-10-24).
func ownerEmailHTML(title, lead string, fields [][2]string, lines []ownerEmailLine, footer string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:Arial,Helvetica,sans-serif;color:#0f172a;max-width:640px;margin:0 auto">`)
	b.WriteString(`<h2 style="font-size:18px;margin:0 0 10px 0">` + html.EscapeString(title) + `</h2>`)
	for _, para := range strings.Split(strings.TrimSpace(lead), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			b.WriteString(`<p style="font-size:14px;line-height:1.5;margin:0 0 12px 0">` + strings.ReplaceAll(html.EscapeString(para), "\n", "<br>") + `</p>`)
		}
	}
	if len(fields) > 0 {
		b.WriteString(`<table style="border-collapse:collapse;font-size:13px;margin:4px 0 14px 0">`)
		for _, f := range fields {
			b.WriteString(`<tr><td style="color:#64748b;padding:3px 14px 3px 0">` + html.EscapeString(f[0]) + `</td><td style="padding:3px 0;font-weight:bold">` + html.EscapeString(f[1]) + `</td></tr>`)
		}
		b.WriteString(`</table>`)
	}
	if len(lines) > 0 {
		b.WriteString(`<table style="border-collapse:collapse;width:100%;font-size:13px;margin:0 0 14px 0">`)
		for _, line := range lines {
			color, label := "#15803d", "Done"
			switch line.status {
			case "failed":
				color, label = "#b91c1c", "Failed"
			case "blocked":
				color, label = "#b45309", "Blocked"
			case "informational":
				color, label = "#475569", "Stopped"
			}
			b.WriteString(`<tr><td style="padding:6px 10px 6px 0;border-top:1px solid #e2e8f0;vertical-align:top;white-space:nowrap"><span style="color:` + color + `;font-weight:bold">` + label + `</span></td>`)
			b.WriteString(`<td style="padding:6px 0;border-top:1px solid #e2e8f0">` + html.EscapeString(line.text) + `</td></tr>`)
		}
		b.WriteString(`</table>`)
	}
	if footer = strings.TrimSpace(footer); footer != "" {
		b.WriteString(`<p style="font-size:12px;color:#64748b;margin:12px 0 0 0">` + html.EscapeString(footer) + `</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
