package step_based_workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func reportToolReadFile(files map[string]string) func(context.Context, string) (string, error) {
	return func(_ context.Context, path string) (string, error) {
		content, ok := files[path]
		if !ok {
			return "", fmt.Errorf("file not found: %s", path)
		}
		return content, nil
	}
}

func validateReport(t *testing.T, html string, hooks ReportHTMLValidationHooks) string {
	t.Helper()
	agent := newWorkshopDefinitionDraft()
	const workspace = "Workflow/demo"
	files := map[string]string{"Workflow/demo/db/reports/index.html": html}
	if err := registerHTMLReportTools(agent, workspace, workshopToolTestLogger{}, reportToolReadFile(files), hooks); err != nil {
		t.Fatalf("registerHTMLReportTools: %v", err)
	}
	tool := agent.tools["validate_report_html"]
	if tool.Execute == nil {
		t.Fatal("validate_report_html was not registered")
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("validate page: %v", err)
	}
	return result
}

func TestValidateHTMLReportRequiresTheSingleWorkflowDocument(t *testing.T) {
	t.Parallel()
	result := validateReport(t, "<!doctype html><html><head><title>Daily briefing</title></head><body>OK</body></html>", ReportHTMLValidationHooks{})
	if !strings.Contains(result, `"valid": true`) || !strings.Contains(result, "Daily briefing") {
		t.Fatalf("unexpected validation result: %s", result)
	}
	if !strings.Contains(result, `"sql_check_enabled": false`) || !strings.Contains(result, `"path_check_enabled": false`) {
		t.Fatalf("expected the result to say the runtime checks were skipped without hooks: %s", result)
	}
}

func TestValidateHTMLReportAcceptsAnotherReportDocument(t *testing.T) {
	t.Parallel()
	agent := newWorkshopDefinitionDraft()
	const workspace = "Workflow/demo"
	files := map[string]string{
		"Workflow/demo/db/reports/tasks.html": "<!doctype html><html><head><title>Tasks</title></head><body>OK</body></html>",
	}
	if err := registerHTMLReportTools(agent, workspace, workshopToolTestLogger{}, reportToolReadFile(files), ReportHTMLValidationHooks{}); err != nil {
		t.Fatal(err)
	}
	result, err := agent.tools["validate_report_html"].Execute(context.Background(), map[string]interface{}{"document_path": "db/reports/tasks.html"})
	if err != nil || !strings.Contains(result, `"path": "db/reports/tasks.html"`) {
		t.Fatalf("validate alternate document: %v %s", err, result)
	}
	if _, err := agent.tools["validate_report_html"].Execute(context.Background(), map[string]interface{}{"document_path": "db/reports/../secret.html"}); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestValidateHTMLReportRunsLiteralQueriesAgainstTheDatabase(t *testing.T) {
	t.Parallel()
	html := "<!doctype html><html><head><title>Runs</title></head><body><script>" +
		"window.report.ready(async function(){" +
		"  var a = await window.report.query('SELECT id FROM runs ORDER BY id DESC LIMIT 5');" +
		"  var b = await window.report.query(\"SELECT count(*) AS n FROM run_summaries\");" +
		"  var c = await window.report.query(`SELECT * FROM leads WHERE status = 'new'`);" +
		"  var d = await window.report.query(`SELECT * FROM ${table}`);" +
		"  var e = await window.report.query(builtSql);" +
		"});</script></body></html>"
	seen := []string{}
	hooks := ReportHTMLValidationHooks{
		ExplainSQL: func(_ context.Context, sql string) error {
			seen = append(seen, sql)
			if strings.Contains(sql, "run_summaries") {
				return errors.New("no such table: run_summaries")
			}
			return nil
		},
	}
	result := validateReport(t, html, hooks)
	if len(seen) != 3 {
		t.Fatalf("expected the three literal queries to be explained, got %d: %v", len(seen), seen)
	}
	if !strings.Contains(result, `"valid": false`) || !strings.Contains(result, "no such table: run_summaries") {
		t.Fatalf("expected the failing query to be reported: %s", result)
	}
	if !strings.Contains(result, `"sql_literals": 3`) || !strings.Contains(result, `"sql_unchecked": 2`) {
		t.Fatalf("expected the result to count checked and unchecked queries: %s", result)
	}
}

// The sales-outreach report (and most agent-authored ones) never call
// window.report.query with a literal: they wrap it once and pass SQL to the
// wrapper. Those statements must still be checked.
func TestValidateHTMLReportChecksSQLPassedThroughALocalWrapper(t *testing.T) {
	t.Parallel()
	html := "<!doctype html><html><head><title>Leads</title></head><body><script>" +
		"async function query(sql){ return normalize(await window.report.query(sql)); }" +
		"async function load(){" +
		"  const leads = await query(`SELECT id, company FROM leads WHERE status = 'new' ORDER BY id DESC`);" +
		"  const gone = await query('select count(*) as n from missing_table');" +
		"  const label = 'Select a market';" + // prose, not SQL
		"  const strategy = \"<!DOCTYPE html><p>select nothing here</p>\";" + // markup, not SQL
		"}</script></body></html>"
	seen := []string{}
	hooks := ReportHTMLValidationHooks{
		ExplainSQL: func(_ context.Context, sql string) error {
			seen = append(seen, sql)
			if strings.Contains(sql, "missing_table") {
				return errors.New("no such table: missing_table")
			}
			return nil
		},
	}
	result := validateReport(t, html, hooks)
	if len(seen) != 2 {
		t.Fatalf("expected the two wrapper-passed statements to be explained, got %d: %v", len(seen), seen)
	}
	if !strings.Contains(result, `"valid": false`) || !strings.Contains(result, "no such table: missing_table") {
		t.Fatalf("expected the failing wrapped query to be reported: %s", result)
	}
	if !strings.Contains(result, `"sql_literals": 2`) {
		t.Fatalf("expected two checked statements: %s", result)
	}
}

func TestValidateHTMLReportChecksReferencedFilesAndAllowsExternalAssets(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><html><head><title>Assets</title>
<link rel="stylesheet" href="https://cdn.example.com/x.css">
</head><body>
<img src="db/assets/logo.png"><a href="db/reports/proof.pdf">proof</a>
<script>window.report.ready(async()=>{ el.innerHTML = await window.report.getHtml('db/notes/summary.md'); window.report.openFile("db/assets/logo.png") })</script>
</body></html>`
	hooks := ReportHTMLValidationHooks{
		FileExists: func(_ context.Context, path string) (bool, error) {
			return path == "db/assets/logo.png", nil
		},
	}
	result := validateReport(t, html, hooks)
	for _, want := range []string{
		`"valid": false`,
		`referenced file \"db/notes/summary.md\" does not exist`,
		`referenced file \"db/reports/proof.pdf\" does not exist`,
		`"referenced_paths": 3`,
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in result: %s", want, result)
		}
	}
	if strings.Contains(result, `\"db/assets/logo.png\" does not exist`) {
		t.Fatalf("existing asset must not be reported: %s", result)
	}
}

func TestValidateHTMLReportAllowsHTTPSCDNStylesAndScripts(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><html data-report-ui="daisyui"><head><title>CDN report</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/daisyui@5.7.38/daisyui.css">
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.7/dist/chart.umd.min.js"></script>
</head><body><canvas id="chart"></canvas><script>new Chart(document.getElementById('chart'), {type: 'bar', data: {labels: [], datasets: []}})</script></body></html>`
	result := validateReport(t, html, ReportHTMLValidationHooks{})
	if !strings.Contains(result, `"valid": true`) {
		t.Fatalf("expected HTTPS CDN assets to validate: %s", result)
	}
	if !strings.Contains(result, `"warnings": []`) {
		t.Fatalf("external library names are the browser's responsibility: %s", result)
	}
}

func TestValidateHTMLReportRejectsUnknownReportMethods(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><html><head><title>API</title></head><body><script>
window.report.ready(async function(){
  var rows = await window.report.query('SELECT 1');
  var x = await window.report.fetchTable('t');
  var y = await report.getRows('t');
  await window.report.chat.send('Do it');
});
</script></body></html>`
	result := validateReport(t, html, ReportHTMLValidationHooks{})
	for _, want := range []string{
		`"valid": false`,
		`window.report.fetchTable does not exist`,
		`window.report.getRows does not exist`,
		`window.report.chat.send does not exist`,
		`available methods`,
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in result: %s", want, result)
		}
	}
	if strings.Contains(result, "window.report.query does not exist") {
		t.Fatalf("known method must not be reported: %s", result)
	}
}

func TestValidateHTMLReportCompositionWidgets(t *testing.T) {
	t.Parallel()
	composed := `<!doctype html><html><head><title>Composed</title></head><body>` +
		`<section id="leads"></section><section id="activity"></section><script>
window.report.ready(async function(){
  await window.report.renderTable('#leads', {query: 'SELECT 1'});
  await window.report.renderActivity('#activity');
  await window.report.chat.mount('#leads');
  await report.chat . open('#leads');
  window.report.chat.close('#leads');
});
</script></body></html>`
	result := validateReport(t, composed, ReportHTMLValidationHooks{})
	if !strings.Contains(result, `"valid": true`) {
		t.Fatalf("composition widgets must validate: %s", result)
	}
	stale := `<!doctype html><html><head><title>Stale</title></head><body><script>
window.report.ready(async function(){
  await window.report.renderEvaluations('#evals');
  await window.report.getEvaluations();
});
</script></body></html>`
	result = validateReport(t, stale, ReportHTMLValidationHooks{})
	for _, want := range []string{
		`"valid": false`,
		`window.report.renderEvaluations does not exist`,
		`window.report.getEvaluations does not exist`,
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in result: %s", want, result)
		}
	}
}

func TestValidateHTMLReportIgnoresCodeLikeTextInStrings(t *testing.T) {
	t.Parallel()
	html := "<!doctype html><html><head><title>Strings</title></head><body><div id=\"a\"></div><script>\n" +
		"var sample = \"document.getElementById('ghost').innerHTML = 'x'; window.report.fakeMethod();\";\n" +
		"// window.report.fakeComment();\n" +
		"function real(){ document.getElementById('a').textContent = sample; }\n" +
		"real();\n" +
		"</script></body></html>"
	result := validateReport(t, html, ReportHTMLValidationHooks{})
	if !strings.Contains(result, `"valid": true`) {
		t.Fatalf("expected code-like strings to validate: %s", result)
	}
	for _, unwanted := range []string{"ghost", "fakeMethod", "fakeComment"} {
		if strings.Contains(result, unwanted) {
			t.Fatalf("unexpected %q in result: %s", unwanted, result)
		}
	}
}

func TestValidateHTMLReportAcceptsHostMarkdownAndAsyncArrows(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><html><head><title>Host</title></head><body><div id="a"></div><script>
function renderMarkdownHtml(md) {
  if (window.report && window.report.renderMarkdown) {
    return window.report.renderMarkdown(md);
  }
  return String(md);
}
async function load(rows) {
  const checks = rows.map(async ([label, path]) => {
    return label + path;
  });
  const done = await Promise.all(checks);
  document.getElementById('a').textContent = renderMarkdownHtml(done.join(','));
}
window.report.ready(function(){ load([]); });
</script></body></html>`
	result := validateReport(t, html, ReportHTMLValidationHooks{})
	if !strings.Contains(result, `"valid": true`) {
		t.Fatalf("expected host methods and async arrows to validate: %s", result)
	}
	for _, unwanted := range []string{"undefined function", "does not exist"} {
		if strings.Contains(result, unwanted) {
			t.Fatalf("unexpected %q in result: %s", unwanted, result)
		}
	}
}

func TestStripReportJSLengthPreserved(t *testing.T) {
	t.Parallel()
	cases := []string{
		`var s = "a'b"; // comment`,
		"var t = 'it\\'s';",
		"var u = `hello ${name} and ${f(x)}`;",
		`var re = /item(\d+)/g; var q = a / b / c;`,
		`return /ab+c/.test(s);`,
		"/* block */ x = 1; <!-- html comment\n--> tail();",
		"var nested = `a${`b${c}`}d`;",
		`el.replace(/'/g, '&#39;');`,
		"var div = a / b / c; var r = x % y;",
	}
	for _, code := range cases {
		stripped, _ := stripReportJS(code)
		if len(stripped) != len(code) {
			t.Fatalf("length changed for %q: %d -> %d (%q)", code, len(code), len(stripped), stripped)
		}
	}
}

func TestStripReportJSKeepsInterpolationsAndBlanksLiterals(t *testing.T) {
	t.Parallel()
	stripped, _ := stripReportJS("var t = `<ul>${rows.map(rowHtml).join('')}</ul>`;")
	if !strings.Contains(stripped, "rows.map(rowHtml)") {
		t.Fatalf("interpolation lost: %q", stripped)
	}
	if strings.Contains(stripped, "<ul>") {
		t.Fatalf("literal text survived stripping: %q", stripped)
	}
	stripped, _ = stripReportJS(`var re = /item(\d+)/g;`)
	if strings.Contains(stripped, "item") {
		t.Fatalf("regex literal survived stripping: %q", stripped)
	}
	stripped, _ = stripReportJS(`var q = a / b;`)
	if !strings.Contains(stripped, "/ b") {
		t.Fatalf("division must survive stripping: %q", stripped)
	}
}

func TestValidateHTMLReportChecksRunScripts(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><html><head><title>Live</title></head><body><div id="deals"></div>
<script>window.report.ready(async()=>{
  const a = await window.report.run('code/reports/deals.py', {days: 7});
  const b = await window.report.run("code/reports/missing.py");
  const c = await window.report.run('db/scripts/x.py');
  document.getElementById('deals').textContent = JSON.stringify([a, b, c]);
})</script>
</body></html>`
	hooks := ReportHTMLValidationHooks{
		FileExists: func(_ context.Context, path string) (bool, error) {
			return path == "code/reports/deals.py", nil
		},
	}
	result := validateReport(t, html, hooks)
	for _, want := range []string{
		`"valid": false`,
		`window.report.run script \"code/reports/missing.py\" does not exist`,
		`window.report.run(\"db/scripts/x.py\"): only a .py or .js script under code/`,
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected %q in result: %s", want, result)
		}
	}
	if strings.Contains(result, `code/reports/deals.py\" does not exist`) || strings.Contains(result, `unknown window.report method`) {
		t.Fatalf("existing script or run() itself flagged: %s", result)
	}
}

// PLAT-750-90: general HTML/JS heuristics must not refuse publication.
// This is platform validation, not a claim that every example executes correctly.
func TestValidateHTMLReportLeavesBrowserBehaviorToPreview(t *testing.T) {
	t.Parallel()
	for name, html := range map[string]string{
		"browser globals":    `<script>new IntersectionObserver(entries => entries.forEach(render)); new FutureBrowserAPI();</script>`,
		"undefined callback": `<script>setTimeout(later, 1); refreshData();</script>`,
		"dynamic DOM":        `<script>document.getElementById('dynamic').textContent = 'ready';</script>`,
		"module order":       `<script type="module">document.getElementById('later').textContent = 'ready';</script><div id="later"></div>`,
		"HTML recovery":      `<html><body>Page</body></html><script>render();</script>`,
		"unclosed script":    `<script>const unfinished = '`,
		"presentation":       `<base href="https://example.com/"><link rel="stylesheet" href="style.css"><style>@media(prefers-color-scheme:dark){body{color:white}}</style><button class="tabbtn">Tab</button>`,
	} {
		t.Run(name, func(t *testing.T) {
			result := validateReport(t, html, ReportHTMLValidationHooks{})
			if !strings.Contains(result, `"valid": true`) || !strings.Contains(result, `"warnings": []`) {
				t.Fatalf("general browser behavior must not be linted: %s", result)
			}
		})
	}
}
