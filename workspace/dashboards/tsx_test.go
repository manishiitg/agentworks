package dashboards

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Exercise the actual durable store, including compile failure, source grants,
// conversion, stable publication identity and restore of an existing HTML page.
func TestTSXDashboardLifecycle(t *testing.T) {
	docs, root := t.TempDir(), "Crew/example"
	if err := os.MkdirAll(filepath.Join(docs, root), 0755); err != nil {
		t.Fatal(err)
	}
	call := func(req Request) Result {
		t.Helper()
		req.Root, req.IncludeDraft = root, true
		out, err := Execute(context.Background(), docs, req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	legacy := `<html><head><title>Existing</title></head><body><button onclick="this.textContent='Working'">Existing</button></body></html>`
	old := call(Request{Action: "create", ID: "overview", Title: "Overview", Files: map[string]string{"index.html": legacy}})
	oldRev := old.Dashboard.Revision
	call(Request{Action: "publish", ID: "overview", ExpectedRevision: oldRev})
	source := `import {useState} from 'react'; import {Card, Metric, Table, LineChart, Button, AskButton, FunctionButton} from '@agentworks/dashboard'; import {Label} from './parts/Label'; import './style.css';
type Summary = {total:number};
export async function loadData(report) { const summary = await report.get('db/summary.json'); const rows = await report.query('SELECT value FROM records WHERE kind = ?', ['recorded']); const result = await report.run('code/summary.py', {}); return {summary,rows,result}; }
export default function Dashboard({data,report}) { const [open,setOpen]=useState(false); return <main><Card title="Shared components"><Label/><Metric label="Recorded total" value={data.summary.total}/><Table columns={['Value']} rows={[[data.summary.total]]}/><LineChart label="Recorded series" data={[{label:"First",value:12},{label:"Second",value:20}]}/><strong id="total">{data.summary.total}</strong><Button label="Details" onPress={()=>setOpen(!open)}/>{open && <p>Recorded details</p>}<p>{data.result.marker}</p><FunctionButton name="lookup" label="Look up"/><AskButton label="Ask"/></Card></main>; }`
	files := map[string]string{"index.tsx": source, "parts/Label.tsx": `export function Label(){ return <h1>Recorded overview</h1> }`, "style.css": `main{padding:24px;max-width:100%;box-sizing:border-box}#total{display:block;color:rgb(10,120,180);font-size:32px}button{margin:8px;padding:12px} @media(max-width:500px){main{padding:12px}}`}
	updated := call(Request{Action: "update", ID: "overview", ExpectedRevision: oldRev, Files: files})
	rev := updated.Dashboard.Revision
	if !strings.Contains(updated.Snapshot.Files["index.html"], `data-report-renderer="tsx"`) || !strings.Contains(updated.Snapshot.Files["dashboard-app.js"], "__agentworksDashboard") || !strings.Contains(updated.Snapshot.Files["dashboard-app.css"], "rgb(10, 120, 180)") {
		t.Fatal("managed compiled output missing")
	}
	for _, broken := range []string{`export default function Broken(){return <main>}`, `import x from '../../private.ts'; export default ()=><p>{x}</p>`, `import x from 'node:fs'; export default ()=><p>{x}</p>`, `export const noDefault = 1`} {
		_, err := Execute(context.Background(), docs, Request{Root: root, Action: "update", ID: "overview", ExpectedRevision: rev, IncludeDraft: true, Files: map[string]string{"index.tsx": broken}})
		if wf.StatusCode(err) != 400 {
			t.Fatalf("bad source accepted: %v", err)
		}
		if call(Request{Action: "get", ID: "overview"}).Dashboard.Revision != rev {
			t.Fatal("compile error changed draft")
		}
	}
	_, err := Execute(context.Background(), docs, Request{Root: root, Action: "update", ID: "overview", ExpectedRevision: rev, IncludeDraft: true, Files: map[string]string{"style.css": "main{padding:20px}"}, Guard: &wf.FolderGuard{WritePaths: []string{"db/reports/managed/overview/index.html"}}})
	if wf.StatusCode(err) != 403 {
		t.Fatalf("source grant widened: %v", err)
	}
	live, err := Execute(context.Background(), docs, Request{Root: root, Action: "get", ID: "overview"})
	if err != nil || live.Snapshot.Files["index.html"] != legacy {
		t.Fatalf("draft changed live page: %v", err)
	}
	call(Request{Action: "publish", ID: "overview", ExpectedRevision: rev})
	if call(Request{Action: "get", ID: "overview"}).Dashboard.DocumentPath != old.Dashboard.DocumentPath {
		t.Fatal("conversion changed dashboard identity")
	}
	if fixture := os.Getenv("DASHBOARD_TSX_TEST_FIXTURE"); fixture != "" {
		page, err := os.ReadFile(filepath.Join(docs, root, Document("overview", rev)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture, page, 0600); err != nil {
			t.Fatal(err)
		}
	}
	call(Request{Action: "restore", ID: "overview", ExpectedRevision: rev, Revision: oldRev})
	if call(Request{Action: "get", ID: "overview"}).Snapshot.Files["index.html"] != legacy {
		t.Fatal("HTML compatibility restore changed")
	}
	call(Request{Action: "restore", ID: "overview", ExpectedRevision: oldRev, Revision: rev})
	if call(Request{Action: "get", ID: "overview"}).Snapshot.Files["index.tsx"] != source {
		t.Fatal("TSX source not retained on restore")
	}
}
