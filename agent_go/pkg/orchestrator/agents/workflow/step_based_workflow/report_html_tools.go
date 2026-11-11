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
// interaction; platform method calls use the same host contract.
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

// reportKnownReportMethods mirrors the win.report object installReportHost
// builds (reportHostRuntime.ts): every method a report may call. workspacePath
// and theme are properties, not calls, so they are not listed.
var reportKnownReportMethods = []string{
	"query", "get", "getText", "getHtml", "renderMarkdown", "fileUrl", "mediaUrl", "openFile",
	"updateField", "updateFields", "run", "getGoalMetrics", "renderGoalProgress",
	"getCosts", "getRelayRuns", "renderCosts", "renderTable", "renderActivity",
	"sendChatMessage", "ready",
	"chat.mount", "chat.open", "chat.close",
}

var (
	reportJSWindowReportCallPattern = regexp.MustCompile(`\bwindow\s*\.\s*report\s*\.\s*([A-Za-z_$][\w$]*(?:\s*\.\s*[A-Za-z_$][\w$]*)?)\s*\(`)
	reportJSBareReportCallPattern   = regexp.MustCompile(`(?:^|[^\w$.])report\s*\.\s*([A-Za-z_$][\w$]*(?:\s*\.\s*[A-Za-z_$][\w$]*)?)\s*\(`)
)

// validateReportHostCalls checks only the injected platform API. JavaScript
// syntax, names, callbacks and DOM behavior belong to the compiler/browser.
func validateReportHostCalls(content string, blocks []reportScriptBlock) []string {
	var fragments []reportHandlerCode
	for _, b := range blocks {
		if !b.hasSrc && !b.data {
			fragments = append(fragments, reportHandlerCode{code: b.code, offset: b.codeStart})
		}
	}
	fragments = append(fragments, reportInlineHandlerCode(content)...)
	known := map[string]bool{}
	for _, method := range reportKnownReportMethods {
		known[method] = true
	}
	seen := map[string]bool{}
	var errors []string
	for _, fragment := range fragments {
		// Ignore method-looking text in comments and literals; this scanner
		// is only for finding host calls, never for judging JS validity.
		stripped, _ := stripReportJS(fragment.code)
		for _, re := range []*regexp.Regexp{reportJSWindowReportCallPattern, reportJSBareReportCallPattern} {
			for _, loc := range re.FindAllStringSubmatchIndex(stripped, -1) {
				method := strings.Join(strings.Fields(stripped[loc[2]:loc[3]]), "")
				if known[method] || seen[method] {
					continue
				}
				seen[method] = true
				errors = append(errors, fmt.Sprintf("window.report.%s does not exist (line %d); available methods: %s", method, reportLineNumber(content, fragment.offset+loc[2]), strings.Join(reportKnownReportMethods, ", ")))
			}
		}
	}
	return errors
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
		"Validate one HTML report document under db/reports/: platform API calls, literal SQL against db/db.sqlite, and referenced workspace files and scripts. This is not an HTML/JavaScript checker; use browser preview for runtime behavior. document_path defaults to db/reports/index.html.",
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
			return ValidateDashboardHTML(ctx, relativePath, content, hooks)
		},
		"workflow",
	)
	logger.Info("✅ Registered HTML report validation tool")
	return nil
}

// ValidateDashboardHTML is shared by app Builder and direct dashboard authoring.
func ValidateDashboardHTML(ctx context.Context, relativePath, content string, hooks ReportHTMLValidationHooks) (string, error) {

	lower := strings.ToLower(content)
	errors := make([]string, 0)
	warnings := make([]string, 0)
	title := ""
	if start := strings.Index(lower, "<title"); start >= 0 {
		if openEnd := strings.Index(lower[start:], ">"); openEnd >= 0 {
			body := content[start+openEnd+1:]
			if end := strings.Index(strings.ToLower(body), "</title>"); end >= 0 {
				title = strings.TrimSpace(body[:end])
			}
		}
	}
	blocks := reportScriptBlocks(content)
	errors = append(errors, validateReportHostCalls(content, blocks)...)

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
			"script_blocks":      len(blocks),
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
}
