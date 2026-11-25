package dashboards

import (
	"encoding/base64"
	"mime"
	"path"
	"regexp"
	"strings"
)

// SelfContained returns an Artifact as one HTML page (owner, 25 Nov): its CSS files go inline as <style>, and every
// other file it names (images, fonts, SVG) becomes a data URL. A copy in the Team's shared folder then renders the
// same as the Artifact itself, in a viewer that only gets the page's text.
func SelfContained(files map[string]string) (string, error) {
	page, ok := files["index.html"]
	if !ok {
		return "", fail(400, "this Artifact has no index.html")
	}
	urls := map[string]string{}
	for p, content := range files {
		if p != "index.html" && strings.ToLower(path.Ext(p)) != ".css" {
			urls[p] = dataURL(p, content)
		}
	}
	inline := func(text string) string {
		for p, u := range urls {
			for _, name := range []string{p, "./" + p} {
				for _, q := range [][2]string{{`"`, `"`}, {`'`, `'`}, {`(`, `)`}} {
					text = strings.ReplaceAll(text, q[0]+name+q[1], q[0]+u+q[1])
				}
			}
		}
		return text
	}
	for p, css := range files {
		if strings.ToLower(path.Ext(p)) != ".css" {
			continue
		}
		link := regexp.MustCompile(`(?i)<link\b[^>]*\bhref=["'](?:\./)?` + regexp.QuoteMeta(p) + `["'][^>]*>`)
		style := "<style>\n" + inline(css) + "\n</style>"
		page = link.ReplaceAllLiteralString(page, style)
	}
	return inline(page), nil
}

func dataURL(p, content string) string {
	if strings.HasPrefix(content, "data:") {
		return content // binary files are already stored as data URLs
	}
	media := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	if strings.ToLower(path.Ext(p)) == ".svg" {
		media = "image/svg+xml"
	}
	if media == "" {
		media = "application/octet-stream"
	}
	return "data:" + media + ";base64," + base64.StdEncoding.EncodeToString([]byte(content))
}
