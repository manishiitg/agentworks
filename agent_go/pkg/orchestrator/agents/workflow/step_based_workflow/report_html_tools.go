package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var (
	reportHTMLIDPattern                = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']+)["']`)
	reportHTMLImmediateDOMWritePattern = regexp.MustCompile(`(?s)document\.getElementById\(\s*["']([^"']+)["']\s*\)\s*\.\s*(?:innerHTML|textContent|style|className|value)\b`)

	// window.report.query('...') / ("...") / (`...`). Only literal SQL is
	// checked; a query built from variables or a template with ${} is reported
	// as unchecked rather than guessed at.
	reportHTMLQueryCallPattern = regexp.MustCompile("(?s)window\\.report\\.query\\(\\s*(?:'((?:[^'\\\\]|\\\\.)*)'|\"((?:[^\"\\\\]|\\\\.)*)\"|`([^`]*)`)")
	// Any JS string literal that reads as a SQL statement. Reports commonly
	// wrap window.report.query in a local helper (`async function query(sql)`)
	// and pass literals to THAT, which the direct-call pattern never sees --
	// measured on a real report: 13 statements, 0 direct calls.
	reportHTMLSQLLiteralPattern = regexp.MustCompile("(?s)(?:'((?:[^'\\\\]|\\\\.)*)'|\"((?:[^\"\\\\]|\\\\.)*)\"|`([^`]*)`)")
	reportHTMLSQLStartPattern   = regexp.MustCompile(`(?i)^\s*(?:select|with|pragma)\b`)
	reportHTMLSQLBodyPattern    = regexp.MustCompile(`(?i)\b(?:from|pragma)\b|^\s*with\b.*\bselect\b`)
	// window.report.get/getText/getHtml/fileUrl/openFile('db/...') plus plain
	// src="db/..." / href="db/..." attributes -- every workspace path a report
	// resolves at runtime.
	reportHTMLPathCallPattern = regexp.MustCompile(`window\.report\.(?:get|getText|getHtml|fileUrl|openFile)\(\s*['"]([^'"]+)['"]`)
	reportHTMLRunCallPattern  = regexp.MustCompile(`window\.report\.run\(\s*['"]([^'"]+)['"]`)
	reportHTMLPathAttrPattern = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*['"]((?:db|knowledgebase|docs|planning|evaluation|costs|variables)/[^'"#?]+)['"]`)
)

func reportHTMLDecodeLiteral(match []string) (sqlText string, dynamic bool) {
	switch {
	case match[1] != "":
		sqlText = strings.ReplaceAll(match[1], `\'`, `'`)
	case match[2] != "":
		sqlText = strings.ReplaceAll(match[2], `\"`, `"`)
	case match[3] != "":
		if strings.Contains(match[3], "${") {
			return "", true
		}
		sqlText = match[3]
	}
	return strings.TrimSpace(sqlText), false
}

// reportHTMLLiteralQueries returns every SQL statement the report holds as a
// string literal -- passed straight to window.report.query or to a local
// wrapper around it -- deduplicated, plus how many direct calls used a
// non-literal (dynamic) query and so cannot be checked.
func reportHTMLLiteralQueries(content string) (literals []string, dynamic int) {
	seen := make(map[string]struct{})
	add := func(sqlText string) {
		if sqlText == "" {
			return
		}
		if _, ok := seen[sqlText]; ok {
			return
		}
		seen[sqlText] = struct{}{}
		literals = append(literals, sqlText)
	}
	direct := 0
	for _, match := range reportHTMLQueryCallPattern.FindAllStringSubmatch(content, -1) {
		sqlText, isDynamic := reportHTMLDecodeLiteral(match)
		if isDynamic {
			dynamic++
			continue
		}
		direct++
		add(sqlText)
	}
	for _, match := range reportHTMLSQLLiteralPattern.FindAllStringSubmatch(content, -1) {
		sqlText, isDynamic := reportHTMLDecodeLiteral(match)
		// "Select a market" is prose; a statement has a FROM (or is a PRAGMA /
		// a WITH that reaches a SELECT). Prose sent to EXPLAIN would fail and
		// wrongly mark the report invalid.
		if isDynamic || !reportHTMLSQLStartPattern.MatchString(sqlText) || !reportHTMLSQLBodyPattern.MatchString(sqlText) {
			continue
		}
		add(sqlText)
	}
	// A direct call whose argument isn't a string literal at all matches
	// neither pattern; count those so the result says how much was NOT checked.
	// (A wrapper call with a literal IS covered by the literal scan above.)
	total := strings.Count(content, "window.report.query(")
	if total > direct+dynamic {
		dynamic += total - direct - dynamic
	}
	return literals, dynamic
}

// reportHTMLReferencedPaths returns every workspace-relative path the report
// resolves at runtime, deduplicated and sorted.
func reportHTMLReferencedPaths(content string) []string {
	seen := make(map[string]struct{})
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || strings.Contains(path, "${") {
			return
		}
		seen[path] = struct{}{}
	}
	for _, match := range reportHTMLPathCallPattern.FindAllStringSubmatch(content, -1) {
		add(match[1])
	}
	for _, match := range reportHTMLPathAttrPattern.FindAllStringSubmatch(content, -1) {
		add(match[1])
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// reportHTMLRunScripts returns every literal script path passed to
// window.report.run, deduplicated and sorted.
func reportHTMLRunScripts(content string) []string {
	seen := make(map[string]struct{})
	for _, match := range reportHTMLRunCallPattern.FindAllStringSubmatch(content, -1) {
		if script := strings.TrimSpace(match[1]); script != "" && !strings.Contains(script, "${") {
			seen[script] = struct{}{}
		}
	}
	scripts := make([]string, 0, len(seen))
	for script := range seen {
		scripts = append(scripts, script)
	}
	sort.Strings(scripts)
	return scripts
}

// reportRunScriptAllowed mirrors the server's window.report.run contract: a
// .py/.js/.mjs script under the workflow's code/ folder.
func reportRunScriptAllowed(scriptPath string) bool {
	clean := path.Clean(scriptPath)
	if clean != scriptPath || !strings.HasPrefix(clean, "code/") || strings.Contains(clean, "/.") {
		return false
	}
	switch path.Ext(clean) {
	case ".py", ".js", ".mjs":
		return true
	}
	return false
}

// ReportHTMLValidationHooks are the runtime-backed checks validate_report_html
// runs on top of its static parse. Either may be nil, in which case that check
// is skipped and the result says so -- the static checks never depend on them.
type ReportHTMLValidationHooks struct {
	// ExplainSQL runs `EXPLAIN <sql>` (or any equivalent read-only prepare)
	// against the workflow's db/db.sqlite and returns the database's own error
	// for a query that would fail at runtime.
	ExplainSQL func(ctx context.Context, sql string) error
	// FileExists reports whether a workspace-relative path (relative to the
	// workflow folder, e.g. "db/assets/logo.png") exists.
	FileExists func(ctx context.Context, relativePath string) (bool, error)
}

// reportScriptBlock is one <script>...</script> element in document order. The
// first literal </script> ends the block even inside a string literal or a
// comment, exactly as the HTML parser treats it -- which is also why a
// "</script>" inside a string silently truncates the block.
type reportScriptBlock struct {
	openStart int
	codeStart int
	codeEnd   int
	hasSrc    bool
	src       string
	data      bool // src or a non-JS type: carries no executable inline code
	closed    bool
	code      string
}

var (
	reportScriptOpenPattern    = regexp.MustCompile(`(?i)<script\b([^>]*)>`)
	reportScriptSrcPattern     = regexp.MustCompile(`(?i)(?:^|\s)src\b(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?`)
	reportScriptTypePattern    = regexp.MustCompile(`(?i)(?:^|\s)type\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	reportInlineHandlerPattern = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reportJSProtocolPattern    = regexp.MustCompile(`(?i)\bhref\s*=\s*(?:"\s*javascript:([^"]*)"|'javascript:([^']*)')`)
	reportHTMLClosePattern     = regexp.MustCompile(`(?i)</html\s*>`)
	reportLinkTagPattern       = regexp.MustCompile(`(?i)<link\b([^>]*)>`)
	reportLinkHrefPattern      = regexp.MustCompile(`(?i)(?:^|\s)href\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	reportLinkRelPattern       = regexp.MustCompile(`(?i)(?:^|\s)rel\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	reportBaseTagPattern       = regexp.MustCompile(`(?i)<base\b`)
)

func reportFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func reportScriptBlocks(content string) []reportScriptBlock {
	lower := strings.ToLower(content)
	var blocks []reportScriptBlock
	for _, loc := range reportScriptOpenPattern.FindAllStringSubmatchIndex(content, -1) {
		openStart, openEnd := loc[0], loc[1]
		attrs := ""
		if loc[2] >= 0 {
			attrs = content[loc[2]:loc[3]]
		}
		block := reportScriptBlock{openStart: openStart, codeStart: openEnd}
		if m := reportScriptSrcPattern.FindStringSubmatch(attrs); m != nil {
			block.hasSrc = true
			block.src = reportFirstNonEmpty(m[1], m[2], m[3])
		}
		jsType := true
		if m := reportScriptTypePattern.FindStringSubmatch(attrs); m != nil {
			t := strings.ToLower(reportFirstNonEmpty(m[1], m[2], m[3]))
			jsType = t == "" || strings.Contains(t, "javascript") || strings.Contains(t, "ecmascript") || strings.Contains(t, "module")
		}
		if idx := strings.Index(lower[openEnd:], "</script"); idx >= 0 {
			block.codeEnd = openEnd + idx
			block.closed = true
		} else {
			block.codeEnd = len(content)
		}
		switch {
		case block.hasSrc:
			// No inline code; the src itself is checked separately.
		case !jsType:
			block.data = true // JSON-LD, import maps: data, not code
		default:
			block.code = content[openEnd:block.codeEnd]
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// reportHandlerCode is executable JS outside <script> blocks: inline event
// handler attributes (onclick="...") and javascript: hrefs. It runs on user
// interaction, so it is always deferred -- but its calls must still resolve.
type reportHandlerCode struct {
	code   string
	offset int
}

func reportInlineHandlerCode(content string) []reportHandlerCode {
	var out []reportHandlerCode
	collect := func(re *regexp.Regexp) {
		for _, loc := range re.FindAllStringSubmatchIndex(content, -1) {
			for g := 2; g+1 < len(loc); g += 2 {
				if loc[g] >= 0 {
					out = append(out, reportHandlerCode{code: content[loc[g]:loc[g+1]], offset: loc[g]})
					break
				}
			}
		}
	}
	collect(reportInlineHandlerPattern)
	collect(reportJSProtocolPattern)
	return out
}

func reportLineNumber(content string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	return strings.Count(content[:offset], "\n") + 1
}

// reportJSStripper blanks the non-code parts of inline report JavaScript --
// comments, string literals, regex literals, and template literal text --
// while preserving template ${} interpolations as code. Every consumed byte
// produces exactly one output byte, so offsets in the stripped text map 1:1
// back onto the original code and a match found on the raw text can be tested
// against the stripped text to tell real code from a string.
type reportJSStripper struct {
	code         string
	out          strings.Builder
	lastSig      byte
	lastWord     string
	unterminated bool
}

func isReportJSIdentChar(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isReportJSIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isReportSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

func stripReportJS(code string) (string, bool) {
	s := &reportJSStripper{code: code}
	s.out.Grow(len(code))
	s.scan(0, false)
	return s.out.String(), s.unterminated
}

func (s *reportJSStripper) blank(n int) {
	for ; n > 0; n-- {
		s.out.WriteByte(' ')
	}
}

func (s *reportJSStripper) emit(b byte) {
	s.out.WriteByte(b)
	if isReportSpace(b) {
		return
	}
	s.lastSig = b
	if isReportJSIdentChar(b) {
		if len(s.lastWord) < 32 {
			s.lastWord += string(b)
		}
	} else {
		s.lastWord = ""
	}
}

// endValue records that the last token is a complete value (a string,
// template, or regex): a slash after a value is division, never a regex.
func (s *reportJSStripper) endValue() {
	s.lastSig = 'v'
	s.lastWord = ""
}

func (s *reportJSStripper) isRegexPosition() bool {
	switch s.lastSig {
	case 0, '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', '}', ';':
		return true
	}
	switch s.lastWord {
	case "return", "typeof", "case", "do", "in", "of", "new", "delete", "void", "instanceof", "yield", "await", "throw":
		return true
	}
	return false
}

// scan consumes s.code from i. When inInterp is true it stops at (returning
// the index of) the closing brace of a ${} interpolation.
func (s *reportJSStripper) scan(i int, inInterp bool) int {
	n := len(s.code)
	depth := 0
	for i < n {
		c := s.code[i]
		if inInterp && (c == '{' || c == '}') {
			if c == '{' {
				depth++
			} else if depth == 0 {
				return i
			} else {
				depth--
			}
			s.emit(c)
			i++
			continue
		}
		switch {
		case c == '/' && i+1 < n && s.code[i+1] == '/':
			j := i + 2
			for j < n && s.code[j] != '\n' {
				j++
			}
			s.blank(j - i)
			i = j
		case c == '/' && i+1 < n && s.code[i+1] == '*':
			j := i + 2
			for j+1 < n && !(s.code[j] == '*' && s.code[j+1] == '/') {
				j++
			}
			if j+1 < n {
				j += 2
			} else {
				j = n
				s.unterminated = true
			}
			s.blank(j - i)
			i = j
		case c == '/' && s.isRegexPosition():
			if end, ok := s.regexEnd(i); ok {
				s.blank(end - i)
				s.endValue()
				i = end
			} else {
				s.emit(c)
				i++
			}
		case c == '\'' || c == '"':
			i = s.skipString(i)
		case c == '`':
			i = s.skipTemplate(i)
		case c == '<' && strings.HasPrefix(s.code[i:], "<!--"):
			// Annex B: <!-- opens a line comment inside scripts.
			j := i + 4
			for j < n && s.code[j] != '\n' {
				j++
			}
			s.blank(j - i)
			i = j
		case c == '-' && strings.HasPrefix(s.code[i:], "-->") && s.onlySpaceSinceLineStart(i):
			j := i + 3
			for j < n && s.code[j] != '\n' {
				j++
			}
			s.blank(j - i)
			i = j
		default:
			s.emit(c)
			i++
		}
	}
	return i
}

func (s *reportJSStripper) skipString(i int) int {
	n := len(s.code)
	quote := s.code[i]
	j := i + 1
	for j < n {
		if s.code[j] == '\\' {
			j += 2
			continue
		}
		if s.code[j] == quote {
			j++
			s.blank(j - i)
			s.endValue()
			return j
		}
		if s.code[j] == '\n' {
			break
		}
		j++
	}
	// Unterminated: a "</script>" (or a real truncation) cut the block inside
	// a string. Blank what was seen and flag it.
	if j > n {
		j = n
	}
	s.unterminated = true
	s.blank(j - i)
	s.endValue()
	return j
}

func (s *reportJSStripper) skipTemplate(i int) int {
	n := len(s.code)
	s.blank(1) // the opening backtick
	i++
	for i < n {
		c := s.code[i]
		if c == '\\' {
			if i+1 < n {
				s.blank(2)
				i += 2
			} else {
				s.blank(1)
				i++
			}
			continue
		}
		if c == '`' {
			s.blank(1)
			s.endValue()
			return i + 1
		}
		if c == '$' && i+1 < n && s.code[i+1] == '{' {
			s.blank(2)
			i = s.scan(i+2, true)
			if i < n && s.code[i] == '}' {
				s.blank(1)
				i++
			} else {
				s.unterminated = true
			}
			continue
		}
		s.blank(1)
		i++
	}
	s.unterminated = true
	s.endValue()
	return i
}

func (s *reportJSStripper) regexEnd(i int) (int, bool) {
	n := len(s.code)
	j := i + 1
	inClass := false
	for j < n {
		c := s.code[j]
		if c == '\\' {
			j += 2
			continue
		}
		if c == '\n' {
			return 0, false
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			j++
			for j < n && isReportJSIdentChar(s.code[j]) {
				j++
			}
			return j, true
		}
		j++
	}
	return 0, false
}

func (s *reportJSStripper) onlySpaceSinceLineStart(i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch s.code[j] {
		case ' ', '\t':
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

// reportJSSpan is a [start, end) byte range of stripped report code.
type reportJSSpan struct{ start, end int }

// reportMatchBracket matches the bracket at s[open] to its closer. The input
// is stripped code, so only code brackets remain and a depth count suffices.
func reportMatchBracket(s string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func reportMatchBracketBack(s string, close int) (int, bool) {
	depth := 0
	for i := close; i >= 0; i-- {
		switch s[i] {
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// reportJSBodySpans returns the function and arrow bodies in stripped code. A
// DOM write inside one runs when the function is called, not when the block
// parses, so ordering checks only apply outside these spans.
func reportJSBodySpans(stripped string) []reportJSSpan {
	var spans []reportJSSpan
	for _, loc := range reportJSFunctionKwPattern.FindAllStringIndex(stripped, -1) {
		i := loc[1]
		for i < len(stripped) && (isReportSpace(stripped[i]) || stripped[i] == '*') {
			i++
		}
		for i < len(stripped) && isReportJSIdentChar(stripped[i]) {
			i++
		}
		for i < len(stripped) && isReportSpace(stripped[i]) {
			i++
		}
		if i >= len(stripped) || stripped[i] != '(' {
			continue
		}
		end, ok := reportMatchBracket(stripped, i)
		if !ok {
			continue
		}
		j := end
		for j < len(stripped) && isReportSpace(stripped[j]) {
			j++
		}
		if j >= len(stripped) || stripped[j] != '{' {
			continue
		}
		if bodyEnd, ok := reportMatchBracket(stripped, j); ok {
			spans = append(spans, reportJSSpan{start: j, end: bodyEnd})
		}
	}
	for idx := 0; idx < len(stripped); {
		next := strings.Index(stripped[idx:], "=>")
		if next < 0 {
			break
		}
		arrow := idx + next
		idx = arrow + 2
		j := arrow + 2
		for j < len(stripped) && isReportSpace(stripped[j]) {
			j++
		}
		if j < len(stripped) && stripped[j] == '{' {
			if end, ok := reportMatchBracket(stripped, j); ok {
				spans = append(spans, reportJSSpan{start: j, end: end})
			}
			continue
		}
		spans = append(spans, reportJSSpan{start: j, end: reportArrowExprEnd(stripped, j)})
	}
	return spans
}

func reportArrowExprEnd(s string, start int) int {
	depth := 0
	for k := start; k < len(s); k++ {
		switch s[k] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return k
			}
		case ';', ',':
			if depth == 0 {
				return k
			}
		}
	}
	return len(s)
}

func reportSpanContains(spans []reportJSSpan, offset int) bool {
	for _, span := range spans {
		if offset >= span.start && offset < span.end {
			return true
		}
	}
	return false
}

var (
	reportJSFunctionKwPattern     = regexp.MustCompile(`\bfunction\b`)
	reportJSFuncDefPattern        = regexp.MustCompile(`\bfunction\s*\*?\s*([A-Za-z_$][\w$]*)`)
	reportJSVarDefPattern         = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)
	reportJSVarDestructurePattern = regexp.MustCompile(`\b(?:const|let|var)\s*([{\[])`)
	reportJSVarRestPattern        = regexp.MustCompile(`,\s*([A-Za-z_$][\w$]*)\s*=`)
	reportJSAssignDefPattern      = regexp.MustCompile(`(?:^|[^\w$.'"])([A-Za-z_$][\w$]*)\s*=\s*[^=]`)
	reportJSCatchPattern          = regexp.MustCompile(`\bcatch\s*\(\s*([A-Za-z_$][\w$]*)`)
	reportJSClassPattern          = regexp.MustCompile(`\bclass\s+([A-Za-z_$][\w$]*)`)
	reportJSWindowAssignPattern   = regexp.MustCompile(`\b(?:window|globalThis|self)\s*\.\s*([A-Za-z_$][\w$]*)\s*=`)
	reportJSImportNamedPattern    = regexp.MustCompile(`\bimport\s*\{([^}]*)\}`)
	reportJSImportDefaultPattern  = regexp.MustCompile(`\bimport\s+([A-Za-z_$][\w$]*)`)
	reportJSImportStarPattern     = regexp.MustCompile(`\bimport\s*\*\s*as\s+([A-Za-z_$][\w$]*)`)
	reportJSIdentPattern          = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// reportJSCallKeywords look like calls (name followed by a paren) but are
// statements, operators, or declarations.
var reportJSCallKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"function": true, "return": true, "typeof": true, "instanceof": true,
	"in": true, "of": true, "new": true, "delete": true, "void": true,
	"do": true, "else": true, "with": true, "await": true, "yield": true,
	"import": true, "super": true, "case": true, "default": true, "extends": true,
	"debugger": true,
}

// reportJSGlobals are callable without any declaration in a classic browser
// script. Deliberately generous: an over-allowed name risks missing a real
// error, an under-allowed one cries wolf on valid code.
var reportJSGlobals = map[string]bool{
	"Array": true, "ArrayBuffer": true, "BigInt": true, "BigInt64Array": true,
	"BigUint64Array": true, "Blob": true, "Boolean": true, "BroadcastChannel": true,
	"CompressionStream": true, "CustomEvent": true, "DOMException": true, "DOMParser": true,
	"DataView": true, "Date": true, "DecompressionStream": true, "Error": true,
	"EvalError": true, "Event": true, "EventSource": true, "File": true, "FileReader": true,
	"Float32Array": true, "Float64Array": true, "FormData": true, "Function": true,
	"Headers": true, "Image": true, "Intl": true, "JSON": true, "Map": true, "Math": true,
	"MessageChannel": true, "MessageEvent": true, "MutationObserver": true, "Number": true,
	"Object": true, "PerformanceObserver": true, "Promise": true, "Proxy": true,
	"RangeError": true, "ReadableStream": true, "ReferenceError": true, "Reflect": true,
	"RegExp": true, "ResizeObserver": true, "Set": true, "String": true, "Symbol": true,
	"SyntaxError": true, "TextDecoder": true, "TextEncoder": true, "TypeError": true,
	"URIError": true, "URL": true, "URLSearchParams": true, "WeakMap": true, "WeakSet": true,
	"WebSocket": true, "Worker": true, "XMLHttpRequest": true, "XMLSerializer": true,
	"AbortController": true, "AbortSignal": true, "AggregateError": true, "Atomics": true,
	"FinalizationRegistry": true, "SharedArrayBuffer": true, "WeakRef": true,
	"WebAssembly": true, "console": true, "crypto": true, "performance": true,
	"navigator": true, "location": true, "history": true, "localStorage": true,
	"sessionStorage": true, "document": true, "window": true, "self": true, "top": true,
	"parent": true, "frames": true, "globalThis": true, "customElements": true,
	"screen": true, "visualViewport": true, "parseInt": true, "parseFloat": true,
	"isNaN": true, "isFinite": true, "encodeURI": true, "encodeURIComponent": true,
	"decodeURI": true, "decodeURIComponent": true, "escape": true, "unescape": true,
	"eval": true, "requestAnimationFrame": true, "cancelAnimationFrame": true,
	"setTimeout": true, "setInterval": true, "clearTimeout": true, "clearInterval": true,
	"queueMicrotask": true, "fetch": true, "getComputedStyle": true, "matchMedia": true,
	"getSelection": true, "alert": true, "confirm": true, "prompt": true, "open": true,
	"close": true, "print": true, "stop": true, "focus": true, "blur": true,
	"scrollTo": true, "scroll": true, "scrollBy": true, "find": true, "postMessage": true,
	"structuredClone": true, "atob": true, "btoa": true, "createImageBitmap": true,
	"addEventListener": true, "removeEventListener": true, "dispatchEvent": true,
	"requestIdleCallback": true, "cancelIdleCallback": true,
	"undefined": true, "NaN": true, "Infinity": true, "this": true, "arguments": true,
	"true": true, "false": true, "null": true,
}

// reportCollectJSDefinitions gathers every name the stripped code defines:
// declarations, assignments (globals in a classic script), parameters, catch
// bindings, classes, window assignments, and imports. When in doubt it
// defines: an extra definition hides at most one error, a missed one invents
// one.
func reportCollectJSDefinitions(stripped string, defined map[string]bool) {
	add := func(name string) {
		if name != "" {
			defined[name] = true
		}
	}
	for _, m := range reportJSFuncDefPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSVarDefPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSVarRestPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSAssignDefPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSCatchPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSClassPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSWindowAssignPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSImportNamedPattern.FindAllStringSubmatch(stripped, -1) {
		for _, part := range strings.Split(m[1], ",") {
			fields := strings.Fields(part)
			switch {
			case len(fields) == 3 && fields[1] == "as":
				add(fields[2])
			case len(fields) == 1:
				add(fields[0])
			}
		}
	}
	for _, m := range reportJSImportDefaultPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	for _, m := range reportJSImportStarPattern.FindAllStringSubmatch(stripped, -1) {
		add(m[1])
	}
	// Destructuring declarations: every identifier inside is usable.
	for _, loc := range reportJSVarDestructurePattern.FindAllStringSubmatchIndex(stripped, -1) {
		if end, ok := reportMatchBracket(stripped, loc[2]); ok {
			for _, name := range reportJSIdentPattern.FindAllString(stripped[loc[2]:end], -1) {
				add(name)
			}
		}
	}
	reportCollectJSParams(stripped, add)
}

func reportCollectJSParams(stripped string, add func(string)) {
	for _, loc := range reportJSFunctionKwPattern.FindAllStringIndex(stripped, -1) {
		i := loc[1]
		for i < len(stripped) && (isReportSpace(stripped[i]) || stripped[i] == '*') {
			i++
		}
		for i < len(stripped) && isReportJSIdentChar(stripped[i]) {
			i++
		}
		for i < len(stripped) && isReportSpace(stripped[i]) {
			i++
		}
		if i >= len(stripped) || stripped[i] != '(' {
			continue
		}
		if end, ok := reportMatchBracket(stripped, i); ok {
			reportDefineParamList(stripped[i+1:end-1], add)
		}
	}
	for idx := 0; idx < len(stripped); {
		next := strings.Index(stripped[idx:], "=>")
		if next < 0 {
			break
		}
		arrow := idx + next
		idx = arrow + 2
		j := arrow - 1
		for j >= 0 && isReportSpace(stripped[j]) {
			j--
		}
		if j < 0 {
			continue
		}
		if stripped[j] == ')' {
			if open, ok := reportMatchBracketBack(stripped, j); ok {
				reportDefineParamList(stripped[open+1:j], add)
			}
			continue
		}
		k := j
		for k >= 0 && isReportJSIdentChar(stripped[k]) {
			k--
		}
		add(stripped[k+1 : j+1])
	}
}

func reportDefineParamList(params string, add func(string)) {
	depth := 0
	start := 0
	flush := func(end int) {
		part := strings.TrimSpace(params[start:end])
		start = end + 1
		if part == "" {
			return
		}
		cut := len(part)
		d := 0
		for i := 0; i < len(part) && cut == len(part); i++ {
			switch part[i] {
			case '(', '[', '{':
				d++
			case ')', ']', '}':
				d--
			case '=':
				if d == 0 {
					cut = i
				}
			}
		}
		head := strings.TrimSpace(part[:cut])
		head = strings.TrimPrefix(head, "...")
		if head == "" {
			return
		}
		if head[0] == '{' || head[0] == '[' {
			for _, name := range reportJSIdentPattern.FindAllString(head, -1) {
				add(name)
			}
			return
		}
		if isReportJSIdentStart(head[0]) {
			i := 1
			for i < len(head) && isReportJSIdentChar(head[i]) {
				i++
			}
			add(head[:i])
		}
	}
	for i := 0; i < len(params); i++ {
		switch params[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				flush(i)
			}
		}
	}
	flush(len(params))
}

// reportJSCall is a bare `name(` in stripped code: not a method call, not the
// tail of a longer identifier.
type reportJSCall struct {
	name  string
	paren int
}

func reportJSCalls(stripped string) []reportJSCall {
	var calls []reportJSCall
	for i := 0; i < len(stripped); i++ {
		if stripped[i] != '(' {
			continue
		}
		j := i - 1
		for j >= 0 && isReportSpace(stripped[j]) {
			j--
		}
		k := j
		for k >= 0 && isReportJSIdentChar(stripped[k]) {
			k--
		}
		name := stripped[k+1 : j+1]
		if name == "" || !isReportJSIdentStart(name[0]) {
			continue
		}
		if k >= 0 && stripped[k] == '.' {
			continue
		}
		calls = append(calls, reportJSCall{name: name, paren: i})
	}
	return calls
}

// reportIsAsyncArrow reports whether `async (` at paren opens an async arrow
// function's parameter list: the matching closer is followed by =>. A bare
// `async` is never a real call, but a user-defined async() still resolves
// through the normal defined set.
func reportIsAsyncArrow(stripped string, paren int) bool {
	end, ok := reportMatchBracket(stripped, paren)
	if !ok {
		return false
	}
	for end < len(stripped) && isReportSpace(stripped[end]) {
		end++
	}
	return end+1 < len(stripped) && stripped[end] == '=' && stripped[end+1] == '>'
}

// reportMethodBody reports whether the call at paren is really a method,
// getter, or setter definition (its argument list is followed by a body
// block) and, if so, that body span. A genuine call is never directly
// followed by a block in agent-authored code.
func reportMethodBody(stripped string, paren int) (reportJSSpan, bool) {
	end, ok := reportMatchBracket(stripped, paren)
	if !ok {
		return reportJSSpan{}, false
	}
	j := end
	for j < len(stripped) && isReportSpace(stripped[j]) {
		j++
	}
	if j >= len(stripped) || stripped[j] != '{' {
		return reportJSSpan{}, false
	}
	bodyEnd, ok := reportMatchBracket(stripped, j)
	if !ok {
		return reportJSSpan{}, false
	}
	return reportJSSpan{start: j, end: bodyEnd}, true
}

// reportKnownReportMethods mirrors the win.report object installReportHost
// builds (reportHostRuntime.ts): every method a report may call. workspacePath
// and theme are properties, not calls, so they are not listed.
var reportKnownReportMethods = []string{
	"query", "get", "getText", "getHtml", "renderMarkdown", "fileUrl", "mediaUrl", "openFile",
	"updateField", "updateFields", "run", "getGoalMetrics", "renderGoalProgress",
	"getCosts", "renderCosts", "renderTable", "renderActivity",
	"sendChatMessage", "ready",
}

var (
	reportJSWindowReportCallPattern = regexp.MustCompile(`\bwindow\s*\.\s*report\s*\.\s*([A-Za-z_$][\w$]*)\s*\(`)
	reportJSBareReportCallPattern   = regexp.MustCompile(`(?:^|[^\w$.])report\s*\.\s*([A-Za-z_$][\w$]*)\s*\(`)
	reportJSReadyArgPattern         = regexp.MustCompile(`\bready\(\s*([A-Za-z_$][\w$]*)\s*\)`)
	reportJSListenerArgPattern      = regexp.MustCompile(`\baddEventListener\(\s*[^,()]*,\s*([A-Za-z_$][\w$]*)\s*[,)]`)
	reportJSTimerArgPattern         = regexp.MustCompile(`\b(?:setTimeout|setInterval|requestAnimationFrame|queueMicrotask)\(\s*([A-Za-z_$][\w$]*)\s*[,)]`)
	reportJSThenArgPattern          = regexp.MustCompile(`\.(?:then|catch|finally)\(\s*([A-Za-z_$][\w$]*)\s*[,)]`)
	reportJSArrayArgPattern         = regexp.MustCompile(`\.(?:map|filter|forEach|find|findIndex|findLast|some|every|reduce|reduceRight|sort)\(\s*([A-Za-z_$][\w$]*)\s*[,)]`)
	reportJSImportWordPattern       = regexp.MustCompile(`(?:^|[^\w$.])import\b`)
	reportJSExportWordPattern       = regexp.MustCompile(`(?:^|[^\w$.])export\b`)
)

type reportDOMWrite struct {
	id    string
	start int
}

// reportDOMWrites finds immediate document.getElementById('...').<sink>
// writes in raw code, skipping matches that sit inside a string literal or
// comment (blank in the stripped twin).
func reportDOMWrites(raw, stripped string) []reportDOMWrite {
	var out []reportDOMWrite
	for _, loc := range reportHTMLImmediateDOMWritePattern.FindAllStringSubmatchIndex(raw, -1) {
		if loc[0] < len(stripped) && stripped[loc[0]] == ' ' {
			continue
		}
		out = append(out, reportDOMWrite{id: raw[loc[2]:loc[3]], start: loc[0]})
	}
	return out
}

func reportIDOffsets(content string) map[string][]int {
	ids := map[string][]int{}
	for _, loc := range reportHTMLIDPattern.FindAllStringSubmatchIndex(content, -1) {
		id := content[loc[2]:loc[3]]
		ids[id] = append(ids[id], loc[0])
	}
	return ids
}

// reportJSUnit is one executable JS fragment: an inline <script> body or an
// inline handler attribute, with its stripped twin (same length, so offsets
// match) and its absolute document offset for line numbers.
type reportJSUnit struct {
	raw      string
	stripped string
	base     int
	handler  bool
}

// validateReportJS checks the report's executable JavaScript: block shape,
// DOM-write targets and their ordering, calls to undefined functions, unknown
// window.report methods, and callback references. Library/loading choices are
// advisory because preview_report is the authoritative browser check.
func validateReportJS(content string, blocks []reportScriptBlock) (errors, warnings []string, scriptCount int) {
	var units []reportJSUnit
	hasExternalScript := false
	for _, b := range blocks {
		if !b.closed {
			errors = append(errors, fmt.Sprintf("script block opened at line %d is never closed with </script>; the rest of the document is swallowed as code", reportLineNumber(content, b.openStart)))
			continue
		}
		if b.hasSrc {
			hasExternalScript = true
			if lower := strings.ToLower(strings.TrimSpace(b.src)); !strings.HasPrefix(lower, "http") && !strings.HasPrefix(lower, "data:") {
				warnings = append(warnings, fmt.Sprintf("script src %q (line %d) is not an absolute HTTPS/data URL; validate its resolution in preview_report", b.src, reportLineNumber(content, b.openStart)))
			}
			continue
		}
		if b.data {
			continue
		}
		stripped, unterminated := stripReportJS(b.code)
		if unterminated {
			errors = append(errors, fmt.Sprintf("script block ending at line %d ends inside a string or comment; a \"</script>\" inside a string ends the block early -- write it as <\\/script>", reportLineNumber(content, b.codeEnd)))
		}
		units = append(units, reportJSUnit{raw: b.code, stripped: stripped, base: b.codeStart})
	}
	for _, h := range reportInlineHandlerCode(content) {
		stripped, _ := stripReportJS(h.code)
		units = append(units, reportJSUnit{raw: h.code, stripped: stripped, base: h.offset, handler: true})
	}

	defined := map[string]bool{}
	for _, u := range units {
		reportCollectJSDefinitions(u.stripped, defined)
	}
	knownReport := map[string]bool{}
	for _, m := range reportKnownReportMethods {
		knownReport[m] = true
	}
	line := func(u reportJSUnit, rel int) int {
		return reportLineNumber(content, u.base+rel)
	}
	resolved := func(name string) bool {
		return defined[name] || reportJSGlobals[name]
	}

	seenCall := map[string]bool{}
	seenReportMethod := map[string]bool{}
	seenCallback := map[string]bool{}
	seenMissingID := map[string]bool{}
	seenOrderID := map[string]bool{}
	seenModule := map[string]bool{}
	ids := reportIDOffsets(content)

	for _, u := range units {
		spans := reportJSBodySpans(u.stripped)
		for _, call := range reportJSCalls(u.stripped) {
			if reportJSCallKeywords[call.name] || resolved(call.name) {
				continue
			}
			if call.name == "async" && reportIsAsyncArrow(u.stripped, call.paren) {
				continue
			}
			if span, ok := reportMethodBody(u.stripped, call.paren); ok {
				spans = append(spans, span)
				continue
			}
			if seenCall[call.name] {
				continue
			}
			seenCall[call.name] = true
			if hasExternalScript {
				warnings = append(warnings, fmt.Sprintf("call to %q (line %d) is not defined inline and may be supplied by an external script; preview the report to verify the CDN library loaded", call.name, line(u, call.paren)))
			} else {
				errors = append(errors, fmt.Sprintf("calls undefined function %q (line %d); define it in a <script> block, fix the spelling, or remove the call", call.name, line(u, call.paren)))
			}
		}
		for _, re := range []*regexp.Regexp{reportJSWindowReportCallPattern, reportJSBareReportCallPattern} {
			for _, loc := range re.FindAllStringSubmatchIndex(u.stripped, -1) {
				method := u.stripped[loc[2]:loc[3]]
				if knownReport[method] || seenReportMethod[method] {
					continue
				}
				seenReportMethod[method] = true
				errors = append(errors, fmt.Sprintf("window.report.%s does not exist (line %d); available methods: %s", method, line(u, loc[2]), strings.Join(reportKnownReportMethods, ", ")))
			}
		}
		for _, re := range []*regexp.Regexp{reportJSReadyArgPattern, reportJSListenerArgPattern, reportJSTimerArgPattern, reportJSThenArgPattern, reportJSArrayArgPattern} {
			for _, loc := range re.FindAllStringSubmatchIndex(u.stripped, -1) {
				name := u.stripped[loc[2]:loc[3]]
				if resolved(name) || seenCallback[name] {
					continue
				}
				seenCallback[name] = true
				if hasExternalScript {
					warnings = append(warnings, fmt.Sprintf("callback %q (line %d) is not defined inline and may be supplied by an external script; preview the report to verify the CDN library loaded", name, line(u, loc[2])))
				} else {
					errors = append(errors, fmt.Sprintf("passes undefined function %q as a callback (line %d); define it or fix the spelling", name, line(u, loc[2])))
				}
			}
		}
		for _, loc := range reportJSImportWordPattern.FindAllStringIndex(u.stripped, -1) {
			if seenModule["import"] {
				break
			}
			j := loc[1]
			for j < len(u.stripped) && isReportSpace(u.stripped[j]) {
				j++
			}
			if j < len(u.stripped) && u.stripped[j] == '.' {
				continue // import.meta
			}
			seenModule["import"] = true
			warnings = append(warnings, fmt.Sprintf("ES module import found (line %d); validate module resolution and CORS in preview_report", line(u, loc[0])))
		}
		for _, loc := range reportJSExportWordPattern.FindAllStringIndex(u.stripped, -1) {
			if seenModule["export"] {
				break
			}
			seenModule["export"] = true
			warnings = append(warnings, fmt.Sprintf("ES module export found (line %d); validate module execution in preview_report", line(u, loc[0])))
		}
		for _, write := range reportDOMWrites(u.raw, u.stripped) {
			offsets, ok := ids[write.id]
			if !ok {
				if !seenMissingID[write.id] {
					seenMissingID[write.id] = true
					errors = append(errors, fmt.Sprintf("script writes to missing element id %q; update the script or restore that element", write.id))
				}
				continue
			}
			if u.handler || seenOrderID[write.id] || reportSpanContains(spans, write.start) {
				continue
			}
			// An immediate write runs when the block parses: the element must
			// already exist above it. Writes inside functions run on call.
			abs := u.base + write.start
			definedAbove := false
			for _, off := range offsets {
				if off < abs {
					definedAbove = true
					break
				}
			}
			if definedAbove {
				continue
			}
			seenOrderID[write.id] = true
			errors = append(errors, fmt.Sprintf("script writes to element id %q before that element appears in the document (line %d); move the <script> below the element or wrap the code in window.report.ready(...)", write.id, line(u, write.start)))
		}
	}
	return errors, warnings, len(blocks)
}

// registerHTMLReportTools exposes the deliberately small report contract. A
// a workflow owns HTML reporting documents under db/reports/. index.html is
// the default, while the shared frontend toolbar handles document navigation.
func registerHTMLReportTools(
	mcpAgent DefinitionToolRegistrar,
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	hooks ReportHTMLValidationHooks,
) error {
	schema := `{"type":"object","properties":{"document_path":{"type":"string","description":"Report HTML path under db/reports/. Defaults to db/reports/index.html."}},"additionalProperties":false}`
	params, err := parseSchemaForToolParameters(schema)
	if err != nil {
		return fmt.Errorf("parse validate_report_html schema: %w", err)
	}

	mcpAgent.RegisterCustomTool(
		"validate_report_html",
		"Validate one HTML report document under db/reports/: document shape, scripted element ids, inline-script placement and ordering, calls to undefined functions and unknown window.report methods, literal SQL against db/db.sqlite, referenced db/ paths, resolvable asset references, and app-theme hooks. HTTPS CDN stylesheets and scripts are allowed. document_path defaults to db/reports/index.html.",
		params,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			relativePath := "db/reports/index.html"
			if raw, ok := args["document_path"].(string); ok && strings.TrimSpace(raw) != "" {
				relativePath = strings.TrimSpace(raw)
			}
			if filepath.ToSlash(relativePath) != relativePath || !strings.HasPrefix(relativePath, "db/reports/") || !strings.HasSuffix(strings.ToLower(relativePath), ".html") {
				return "", fmt.Errorf("document_path must be a canonical .html path under db/reports/")
			}
			for _, part := range strings.Split(relativePath, "/") {
				if part == "" || part == "." || part == ".." {
					return "", fmt.Errorf("document_path must be a canonical .html path under db/reports/")
				}
			}
			content, err := readFile(ctx, filepath.ToSlash(filepath.Join(workspacePath, relativePath)))
			if err != nil {
				return "", fmt.Errorf("read %s: %w", relativePath, err)
			}
			lower := strings.ToLower(content)
			errors := make([]string, 0)
			warnings := make([]string, 0)
			if !strings.Contains(lower, "<html") {
				warnings = append(warnings, "missing explicit <html> root; the browser will synthesize one, but a complete document is easier to inspect")
			}
			if !strings.Contains(lower, "<body") {
				warnings = append(warnings, "missing explicit <body>; the browser will synthesize one, but a complete document is easier to inspect")
			}
			title := ""
			if start := strings.Index(lower, "<title"); start >= 0 {
				if openEnd := strings.Index(lower[start:], ">"); openEnd >= 0 {
					body := content[start+openEnd+1:]
					if end := strings.Index(strings.ToLower(body), "</title>"); end >= 0 {
						title = strings.TrimSpace(body[:end])
					}
				}
			}
			if title == "" {
				warnings = append(warnings, "missing non-empty <title>; add one for toolbar naming and accessibility")
			}
			blocks := reportScriptBlocks(content)
			jsErrors, jsWarnings, scriptCount := validateReportJS(content, blocks)
			errors = append(errors, jsErrors...)
			warnings = append(warnings, jsWarnings...)

			// Content after </html> is silently dropped by the browser: a
			// <script> pasted there never runs.
			if closes := reportHTMLClosePattern.FindAllStringIndex(content, -1); len(closes) > 0 {
				if rest := strings.TrimSpace(content[closes[len(closes)-1][1]:]); rest != "" {
					errors = append(errors, fmt.Sprintf("content after </html> is ignored by the browser (line %d); move it inside the document", reportLineNumber(content, closes[len(closes)-1][1])))
				}
			}

			// A <base> tag is valid, but it changes every relative URL. Make the
			// choice visible and leave runtime resolution to preview_report.
			if loc := reportBaseTagPattern.FindStringIndex(content); loc != nil {
				warnings = append(warnings, fmt.Sprintf("a <base> tag repoints relative URLs (line %d); verify all linked assets and navigation in preview_report", reportLineNumber(content, loc[0])))
			}

			// Subresources never resolve inside the sandboxed srcdoc report;
			// https URLs are already an error above, data: URLs are fine.
			for _, loc := range reportLinkTagPattern.FindAllStringSubmatchIndex(content, -1) {
				attrs := content[loc[2]:loc[3]]
				href := ""
				if m := reportLinkHrefPattern.FindStringSubmatch(attrs); m != nil {
					href = reportFirstNonEmpty(m[1], m[2], m[3])
				}
				if href == "" || strings.HasPrefix(href, "data:") || strings.HasPrefix(strings.ToLower(href), "http") {
					continue
				}
				rel := ""
				if m := reportLinkRelPattern.FindStringSubmatch(attrs); m != nil {
					rel = strings.ToLower(reportFirstNonEmpty(m[1], m[2], m[3]))
				}
				tagLine := reportLineNumber(content, loc[0])
				if strings.Contains(rel, "stylesheet") {
					warnings = append(warnings, fmt.Sprintf("stylesheet link %q is relative (line %d); verify that it resolves in preview_report", href, tagLine))
				} else {
					warnings = append(warnings, fmt.Sprintf("resource link %q is ignored inside the sandboxed report (line %d); drop the tag or inline the asset", href, tagLine))
				}
			}

			// Theme: the Report tab mirrors the APP theme onto the document as
			// `.dark` + `data-theme`, not the OS scheme. A report that only keys
			// off prefers-color-scheme ignores the in-app toggle.
			usesOSScheme := strings.Contains(lower, "prefers-color-scheme")
			usesAppTheme := strings.Contains(lower, ".dark") || strings.Contains(lower, "data-theme") || strings.Contains(lower, "report:theme") || strings.Contains(lower, "var(--")
			if usesOSScheme && !usesAppTheme {
				warnings = append(warnings, "dark mode keys only off prefers-color-scheme (the OS), so it ignores the app's light/dark toggle; key off `:root.dark` / `[data-theme=\"dark\"]` or the injected hsl(var(--token)) palette instead")
			}

			// Tabs: view-switch buttons without tab semantics leave
			// screen-reader and keyboard users with no selected-state
			// announcement.
			hasTabButtons := strings.Contains(lower, "<button") &&
				(strings.Contains(content, "data-report-view") ||
					strings.Contains(lower, "tabbtn") ||
					strings.Contains(lower, "tab-button"))
			hasTabRoles := strings.Contains(lower, "role=\"tab\"") ||
				strings.Contains(lower, "role='tab'") ||
				strings.Contains(lower, "aria-selected")
			if hasTabButtons && !hasTabRoles {
				warnings = append(warnings, "view-switch buttons have no tab semantics; add role=\"tablist\"/role=\"tab\", aria-selected toggling, and arrow-key support")
			}

			// Activity: a hand-rolled feed over the run summaries duplicates
			// the platform widget. Custom merged timelines stay valid; this
			// is a nudge, not a mandate.
			queriesActivity := strings.Contains(content, "org_dashboard_notifications")
			buildsRows := strings.Contains(content, "innerHTML") ||
				strings.Contains(content, "insertAdjacentHTML") ||
				strings.Contains(content, "createElement")
			if queriesActivity && buildsRows && !strings.Contains(content, "renderActivity") {
				warnings = append(warnings, "hand-rolled activity feed over org_dashboard_notifications; consider window.report.renderActivity(target) for the standard route-grouped section (custom merged timelines remain valid)")
			}

			// Live SQL: run each literal query through the database so a typo'd
			// table or column fails here, not silently in the Report tab.
			queries, dynamicQueries := reportHTMLLiteralQueries(content)
			sqlChecked := 0
			if hooks.ExplainSQL != nil {
				for _, sqlText := range queries {
					sqlChecked++
					if err := hooks.ExplainSQL(ctx, sqlText); err != nil {
						preview := sqlText
						if len(preview) > 160 {
							preview = preview[:160] + "…"
						}
						errors = append(errors, fmt.Sprintf("window.report.query SQL fails against db/db.sqlite: %v -- %s", err, preview))
					}
				}
			}

			// Referenced files: a path that is not under the workflow shows a
			// broken image and nothing else at runtime.
			referenced := reportHTMLReferencedPaths(content)
			pathsChecked := 0
			if hooks.FileExists != nil {
				for _, path := range referenced {
					pathsChecked++
					exists, err := hooks.FileExists(ctx, path)
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("could not check referenced path %q: %v", path, err))
						continue
					}
					if !exists {
						errors = append(errors, fmt.Sprintf("referenced file %q does not exist in the workflow folder; publish it under db/ or fix the path", path))
					}
				}
			}
			// Live-data scripts: the server only runs scripts under code/.
			runScripts := reportHTMLRunScripts(content)
			for _, script := range runScripts {
				if !reportRunScriptAllowed(script) {
					errors = append(errors, fmt.Sprintf("window.report.run(%q): only a .py or .js script under code/ can run (e.g. code/reports/pipeline.py)", script))
					continue
				}
				if hooks.FileExists == nil {
					continue
				}
				pathsChecked++
				referenced = append(referenced, script)
				exists, err := hooks.FileExists(ctx, script)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("could not check window.report.run script %q: %v", script, err))
				} else if !exists {
					errors = append(errors, fmt.Sprintf("window.report.run script %q does not exist; write it under code/ first and run it once with REPORT_ARGS set", script))
				}
			}

			result := map[string]interface{}{
				"valid":    len(errors) == 0,
				"path":     relativePath,
				"title":    title,
				"bytes":    len(content),
				"errors":   errors,
				"warnings": warnings,
				"checked": map[string]interface{}{
					"sql_literals":       sqlChecked,
					"sql_unchecked":      dynamicQueries + (len(queries) - sqlChecked),
					"referenced_paths":   pathsChecked,
					"paths_unchecked":    len(referenced) - pathsChecked,
					"script_blocks":      scriptCount,
					"sql_check_enabled":  hooks.ExplainSQL != nil,
					"path_check_enabled": hooks.FileExists != nil,
				},
				"next_step":     "Open the Report tab to verify layout and scrolling.",
				"page_contract": "Each db/reports/*.html document owns its internal layout; the shared toolbar selects documents.",
			}
			out, marshalErr := json.MarshalIndent(result, "", "  ")
			if marshalErr != nil {
				return "", fmt.Errorf("marshal report validation: %w", marshalErr)
			}
			return string(out), nil
		},
		"workflow",
	)
	logger.Info("✅ Registered HTML report validation tool")
	return nil
}
