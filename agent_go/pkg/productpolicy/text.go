package productpolicy

import (
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Text projects explicit product-owned blocks in trusted prompts, descriptions
// and skill references. It never removes arbitrary user text by keyword. Blocks
// may nest; malformed/unclosed blocks fail closed rather than leaking guidance.
// Syntax: <!-- product:mcp-gateway --> ... <!-- /product -->
func (s Selection) Text(text string) string {
	const open = "<!-- product:"
	const close = "<!-- /product -->"
	var out strings.Builder
	allowed := []bool{true}
	positions := []int{0}
	for len(text) > 0 {
		start, end := strings.Index(text, open), strings.Index(text, close)
		if start < 0 && end < 0 {
			if allowed[len(allowed)-1] {
				out.WriteString(text)
			}
			break
		}
		if end >= 0 && (start < 0 || end < start) {
			if allowed[len(allowed)-1] {
				out.WriteString(text[:end])
			}
			if len(allowed) > 1 {
				allowed = allowed[:len(allowed)-1]
				positions = positions[:len(positions)-1]
			}
			text = text[end+len(close):]
			continue
		}
		if allowed[len(allowed)-1] {
			out.WriteString(text[:start])
		}
		text = text[start+len(open):]
		stop := strings.Index(text, "-->")
		if stop < 0 {
			break
		}
		product := strings.TrimSpace(text[:stop])
		allowed = append(allowed, allowed[len(allowed)-1] && product != "" && s.Has(product))
		positions = append(positions, out.Len())
		text = text[stop+3:]
	}
	result := out.String()
	if len(positions) > 1 {
		result = result[:positions[1]]
	}
	return result
}

// Schema makes a defensive projection, including nested property descriptions.
func (s Selection) Schema(value any) any {
	switch v := value.(type) {
	case string:
		return s.Text(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = s.Schema(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = s.Schema(x)
		}
		return out
	default:
		return value
	}
}
func (s Selection) Skill(skill *llmtypes.Skill) *llmtypes.Skill {
	if skill == nil || !s.AllowsSkill(skill.Name) {
		return nil
	}
	// Only trusted platform bundles use conditional blocks. Imported skills and
	// workflow learnings remain owned by the project and are preserved verbatim.
	if skill.Source.Origin != "builtin" {
		return skill
	}
	copy := *skill
	copy.Description, copy.Content = s.Text(skill.Description), s.Text(skill.Content)
	copy.SupportingFiles = nil
	for _, file := range skill.SupportingFiles {
		if utf8.Valid(file.Content) {
			body := s.Text(string(file.Content))
			if strings.TrimSpace(body) == "" {
				continue
			}
			file.Content = []byte(body)
		}
		copy.SupportingFiles = append(copy.SupportingFiles, file)
	}
	return &copy
}
