package admin

import (
	"errors"
	"html/template"
	"net/http"
)

var errNoSubject = errors.New("pick a user or a group")

const pageCSS = `body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;max-width:64rem;margin:0 auto;padding:1rem 1.5rem 4rem;color:#1a1a1a;background:#fff;line-height:1.45}
nav{border-bottom:2px solid #1a1a1a;padding:.5rem 0;margin-bottom:1.5rem}
nav a{margin-right:1rem;color:#1a1a1a}
h1{font-size:1.4rem;margin:.2rem 0 1rem}
h2{font-size:1.1rem;margin:2rem 0 .5rem;border-bottom:1px solid #ddd;padding-bottom:.25rem}
table{border-collapse:collapse;width:100%;margin:.5rem 0}
th,td{border:1px solid #ccc;padding:.35rem .6rem;text-align:left;font-size:.9rem;vertical-align:top}
th{background:#f2f2f2}
code{font-size:.85em;background:#f2f2f2;padding:.1em .3em}
form.inline{display:inline}
input,select,button{font:inherit;padding:.25rem .5rem;margin:.1rem .25rem .1rem 0}
.err{background:#fde8e8;border:1px solid #c00;padding:.5rem .75rem;margin-bottom:1rem}
.muted{color:#555;font-size:.85rem}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax:9rem,1fr));gap:.75rem;margin:1rem 0}
.stat{border:1px solid #ccc;padding:.75rem}
.stat b{font-size:1.5rem;display:block}
.tag{font-size:.75rem;border:1px solid #888;padding:0 .35rem;margin-left:.4rem;white-space:nowrap}`

const pageNav = `<nav><strong>Vault</strong> · MCP Gateway &nbsp; <a href="/admin/">dashboard</a><a href="/admin/connectors">connectors</a><a href="/admin/tools">tools</a><a href="/admin/users">users</a><a href="/admin/groups">groups</a><a href="/admin/pii">PII policy</a><a href="/admin/audit">audit</a><form class="inline" method="post" action="/admin/logout"><button type="submit">Sign out</button></form></nav>`

var pages = template.Must(template.New("admin").Funcs(template.FuncMap{"list": func(values ...string) []string { return values }}).Parse(`
{{define "dashboard"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Dashboard</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>Dashboard</h1>
<div class="grid">
<div class="stat"><b>{{.Users}}</b>users</div>
<div class="stat"><b>{{.Groups}}</b>groups</div>
<div class="stat"><b>{{.Connectors}}</b>connectors</div>
<div class="stat"><b>{{.Tools}}</b>tools</div>
<div class="stat"><b>{{.Grants}}</b>grants</div>
<div class="stat"><b>{{.Audit}}</b>audit events</div>
</div>
<h2>Connect a client</h2>
<p>MCP endpoint: <code>{{.MCPURL}}</code></p>
<p class="muted">Clients sign in through OAuth discovery. Claude and other clients find the endpoints automatically from this URL.</p>
</body></html>{{end}}

{{define "users"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Users</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>Users</h1>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
<table><tr><th>id</th><th>email</th><th>groups</th><th>direct grants</th></tr>
{{range .Rows}}<tr><td><code>{{.ID}}</code></td><td>{{.Email}}</td><td>{{range .Groups}}<code>{{.}}</code> {{end}}</td><td>{{range .Grants}}<code>{{.}}</code><br>{{end}}</td></tr>{{end}}
</table>
<h2>Add user</h2>
<form method="post" action="/admin/users/add"><input name="id" placeholder="user id" required> <input name="email" placeholder="email" size="30" required> <button>Add</button></form>
</body></html>{{end}}

{{define "groups"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Groups</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>Groups</h1>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
{{range .Rows}}{{$gid := .ID}}
<h2><code>{{.ID}}</code> — {{.Name}}</h2>
{{if .Description}}<p class="muted">{{.Description}}</p>{{end}}
<p><span class="muted">Members:</span> {{range .Members}}<code>{{.}}</code> <form class="inline" method="post" action="/admin/groups/members"><input type="hidden" name="group" value="{{$gid}}"><input type="hidden" name="user" value="{{.}}"><input type="hidden" name="action" value="remove"><button title="remove">×</button></form>{{else}}<span class="muted">none</span>{{end}}</p>
<form method="post" action="/admin/groups/members"><input type="hidden" name="group" value="{{.ID}}"><select name="user">{{range $.Users}}<option value="{{.ID}}">{{.ID}}</option>{{end}}</select> <button>Add member</button></form>
<p><span class="muted">Tool grants:</span> {{range .Grants}}<code>{{.}}</code> {{else}}<span class="muted">none</span>{{end}}</p>
<form method="post" action="/admin/grants/set"><input type="hidden" name="group" value="{{.ID}}"><select name="tool">{{range $.Tools}}<option value="{{.PublicName}}">{{.PublicName}}</option>{{end}}</select> <button name="action" value="grant">Grant</button> <button name="action" value="revoke">Revoke</button></form>
{{end}}
<h2>Add group</h2>
<form method="post" action="/admin/groups/add"><input name="id" placeholder="group id" required> <input name="name" placeholder="display name" required> <input name="description" placeholder="description (optional)" maxlength="1000"> <button>Add</button></form>
</body></html>{{end}}

{{define "connectors"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Connectors</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>Connectors</h1>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
<table><tr><th>label</th><th>provider</th><th>instance</th><th>upstream</th><th>status</th><th>tools</th><th></th></tr>
{{range .Rows}}<tr><td>{{.Label}}</td><td><code>{{.Provider}}</code></td><td><code>{{.InstanceSlug}}</code></td><td class="muted">{{.UpstreamURL}}</td><td>{{.Status}}</td><td>{{.ToolCount}}</td>
<td><form class="inline" method="post" action="/admin/connectors/sync"><input type="hidden" name="id" value="{{.ID}}"><button>sync</button></form>
<form class="inline" method="post" action="/admin/connectors/delete" onsubmit="return confirm('Remove {{.Label}}?')"><input type="hidden" name="id" value="{{.ID}}"><button>remove</button></form></td></tr>{{end}}
</table>
<h2>Add from catalog</h2>
<form method="post" action="/admin/connectors/add"><select name="provider">{{range .Providers}}<option value="{{.Name}}">{{.Name}}{{if .OAuth}} (OAuth){{end}}</option>{{end}}</select>
<input name="label" placeholder="label"> <input name="slug" placeholder="instance slug (optional)" size="20"> <button>Add + sync</button></form>
<p class="muted">OAuth providers need per-user sign-in, which lands in M2 — only no-auth providers connect today.</p>
<h2>Add custom URL</h2>
<form method="post" action="/admin/connectors/add"><input type="hidden" name="mode" value="custom"><input name="provider" placeholder="provider key" required> <input name="url" placeholder="https://…/mcp" size="40" required> <input name="label" placeholder="label"> <input name="slug" placeholder="slug" size="12"> <button>Add + sync</button></form>
</body></html>{{end}}

{{define "tools"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Tools</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>Tools</h1>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
<table><tr><th>public name</th><th>connector</th><th>upstream</th><th>status</th><th>users</th><th>groups</th><th>grant</th><th>review</th></tr>
{{range .Rows}}<tr><td><code>{{.PublicName}}</code></td><td>{{.ConnLabel}}</td><td><code>{{.UpstreamName}}</code></td><td>{{.Status}} v{{.Version}}</td>
<td>{{range .Users}}<code>{{.}}</code> {{end}}</td><td>{{range .Groups}}<code>{{.}}</code> {{end}}</td>
<td><form class="inline" method="post" action="/admin/grants/set"><input type="hidden" name="tool" value="{{.PublicName}}">
<select name="user"><option value="">user…</option>{{range $.Users}}<option value="{{.ID}}">{{.ID}}</option>{{end}}</select><button name="action" value="grant">+</button><button name="action" value="revoke">−</button></form>
<form class="inline" method="post" action="/admin/grants/set"><input type="hidden" name="tool" value="{{.PublicName}}">
<select name="group"><option value="">group…</option>{{range $.Groups}}<option value="{{.ID}}">{{.ID}}</option>{{end}}</select><button name="action" value="grant">+</button><button name="action" value="revoke">−</button></form></td>
<td><details><summary>Definition</summary><p>{{.Description}}</p><pre>{{printf "%s" .InputSchema}}</pre>
{{range .Previous}}<p>Previous v{{.Version}}: {{.Description}}</p><pre>{{printf "%s" .InputSchema}}</pre>{{end}}</details>
{{if eq .Status "quarantined"}}<form method="post" action="/admin/tools/approve"><input type="hidden" name="name" value="{{.PublicName}}"><input type="hidden" name="version" value="{{.Version}}"><input type="hidden" name="fingerprint" value="{{.Fingerprint}}"><button>Approve v{{.Version}}</button></form>{{end}}</td></tr>{{end}}
</table>
</body></html>{{end}}

{{define "audit"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Audit</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>Audit (latest first)</h1>
<form method="get"><input name="user" placeholder="user" value="{{.Filter.Get "user"}}"><input name="group" placeholder="group" value="{{.Filter.Get "group"}}"><input name="client" placeholder="client" value="{{.Filter.Get "client"}}"><input name="connector" placeholder="connector" value="{{.Filter.Get "connector"}}"><input name="tool" placeholder="tool" value="{{.Filter.Get "tool"}}"><select name="decision"><option value="">any decision</option><option value="allow">allow</option><option value="deny">deny</option></select><select name="outcome"><option value="">any outcome</option><option value="ok">ok</option><option value="denied">denied</option><option value="upstream_error">upstream error</option></select><input type="date" name="after" value="{{.Filter.Get "after"}}"><input type="date" name="before" value="{{.Filter.Get "before"}}"><button>Filter</button></form>
<p><a href="{{.CSVURL}}">Export filtered CSV</a> · <a href="{{.JSONURL}}">Export filtered JSON</a></p>
<h2>Usage history</h2><p>{{.Usage.Total}} calls · {{.Usage.Allowed}} allowed · {{.Usage.Denied}} denied · {{.Usage.UpstreamErrors}} upstream errors · {{.Usage.AvgDurationMs}} ms average</p>
<table><tr><th>day (UTC)</th><th>calls</th><th>denied</th><th>upstream errors</th></tr>{{range .Usage.ByDay}}<tr><td>{{.Key}}</td><td>{{.Count}}</td><td>{{.Denied}}</td><td>{{.UpstreamErrors}}</td></tr>{{end}}</table>
<h3>Most used tools</h3><table><tr><th>tool</th><th>calls</th><th>denied</th></tr>{{range .Usage.ByTool}}<tr><td><code>{{.Key}}</code></td><td>{{.Count}}</td><td>{{.Denied}}</td></tr>{{end}}</table>
<h2>Call events (latest first)</h2>
<table><tr><th>time</th><th>user</th><th>groups</th><th>client</th><th>tool</th><th>decision</th><th>outcome</th><th>PII</th><th>ms</th><th>error</th></tr>
{{range .Rows}}<tr><td class="muted">{{.Timestamp.Format "2006-01-02 15:04:05"}}</td><td><code>{{.UserID}}</code></td><td>{{range .GroupIDs}}{{.}} {{end}}</td><td>{{.ClientID}}</td><td><code>{{.PublicName}}</code></td><td>{{.Decision}}</td><td>{{.Outcome}}</td><td>{{.PIIAction}}</td><td>{{.DurationMs}}</td><td class="muted">{{.ErrorText}}</td></tr>{{end}}
</table>
</body></html>{{end}}

{{define "pii"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · PII policy</title><style>` + pageCSS + `</style></head><body>` + pageNav + `
<h1>PII policy</h1>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
<p class="muted">Deterministic regex and checksum checks. Email and US phone numbers are masked by default; SSNs, valid credit cards, and known API key formats are blocked. Opaque results are blocked. Detection is best effort.</p>
<h2>Rules</h2>
<table><tr><th>type</th><th>direction</th><th>action</th><th>scope</th><th></th></tr>
{{range .Rules}}<tr><td>{{.DataType}}</td><td>{{.Direction}}</td><td>{{.Action}}</td><td>{{.GroupID}} / {{.ConnectorID}} / {{.PublicName}}</td><td><a href="/admin/pii?edit={{.ID}}">edit</a> <form class="inline" method="post" action="/admin/pii/rules/delete"><input type="hidden" name="id" value="{{.ID}}"><button>delete</button></form></td></tr>{{end}}
</table>
<h2>{{if .Edit.ID}}Edit rule{{else}}Add rule{{end}}</h2>
<form method="post" action="/admin/pii/rules/save"><input type="hidden" name="id" value="{{.Edit.ID}}">
<select name="type">{{range $type := (list "email" "phone" "ssn" "credit_card" "api_key")}}<option value="{{$type}}" {{if eq $.Edit.DataType $type}}selected{{end}}>{{$type}}</option>{{end}}</select>
<select name="direction">{{range $direction := (list "input" "output" "both")}}<option value="{{$direction}}" {{if eq $.Edit.Direction $direction}}selected{{end}}>{{$direction}}</option>{{end}}</select>
<select name="action">{{range $action := (list "allow" "mask" "block" "require_review")}}<option value="{{$action}}" {{if eq $.Edit.Action $action}}selected{{end}}>{{$action}}</option>{{end}}</select><br>
<p class="muted">Require review applies to input only. Output matches are blocked to avoid repeating an upstream action.</p>
<select name="group"><option value="">all groups</option>{{range .Groups}}<option value="{{.ID}}" {{if eq $.Edit.GroupID .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select>
<select name="connector"><option value="">all servers</option>{{range .Connectors}}<option value="{{.ID}}" {{if eq $.Edit.ConnectorID .ID}}selected{{end}}>{{.Label}}</option>{{end}}</select>
<select name="tool"><option value="">all tools</option>{{range .Tools}}<option value="{{.PublicName}}" {{if eq $.Edit.PublicName .PublicName}}selected{{end}}>{{.PublicName}}</option>{{end}}</select>
<button>Save rule</button></form>
<h2>Test a sample</h2><p class="muted">The sample is inspected on demand and not saved to the audit log.</p>
<form method="post" action="/admin/pii/test"><textarea name="sample" rows="3" cols="70" placeholder="Paste a sample value" required></textarea><br><select name="direction"><option value="input">input</option><option value="output">output</option></select><button>Test policy</button></form>
{{if .Result}}<p><strong>Result:</strong> {{.Result}}</p>{{end}}
<h2>Pending reviews</h2><p class="muted">Approval permits one matching client retry; the gateway does not replay a call.</p>
<table><tr><th>call</th><th>user</th><th>tool</th><th>direction</th><th>types</th><th>status</th><th></th></tr>
{{range .Reviews}}<tr><td><code>{{.ID}}</code></td><td>{{.UserID}}</td><td>{{.PublicName}}</td><td>{{.Direction}}</td><td>{{range .DataTypes}}{{.}} {{end}}</td><td>{{.Status}}</td><td>{{if eq .Status "pending"}}<form method="post" action="/admin/pii/reviews/approve"><input type="hidden" name="id" value="{{.ID}}"><button>Approve retry</button></form>{{end}}</td></tr>{{end}}</table>
</body></html>{{end}}

{{define "login"}}<!doctype html><html><head><meta charset="utf-8"><title>Vault · Admin login</title></head>
<body style="font-family:system-ui;max-width:24rem;margin:4rem auto">
<h1>Vault</h1><p>MCP Gateway admin</p>
{{if .Err}}<p style="color:red">Wrong token.</p>{{end}}
<form method="post"><input type="password" name="token" size="32" placeholder="admin token" autofocus>
<button>Sign in</button></form>
</body></html>{{end}}
`))

func render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, page, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
