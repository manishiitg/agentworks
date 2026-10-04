package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The helper is exercised against a fake bridge that speaks the real HTTP shape:
// POST <MCP_CUSTOM>/<tool> with the MCP_AUTH header, answering
// {"success":true,"result":"<the tool's JSON as a string>"} or {"success":false,"error":...}.
func TestDatabaseHelper(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agentworks_db.py"), dbRuntime, 0600); err != nil {
		t.Fatal(err)
	}
	script := `
import datetime, decimal, json, os, threading
from http.server import BaseHTTPRequestHandler, HTTPServer

ROWS = [{"id": i, "label": "row-%d" % i} for i in range(1234)]
calls = []
state = {"fail_query_once": False, "bad_paging": False}

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def _reply(self, code, payload):
        data = json.dumps(payload).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def do_POST(self):
        tool = self.path.strip("/")
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        calls.append((tool, body, self.headers.get("Authorization")))
        if self.headers.get("Authorization") != "Bearer tok":
            return self._reply(401, {"success": False, "error": "unauthorized"})
        if tool == "query_workflow_db":
            if state["fail_query_once"]:
                state["fail_query_once"] = False
                return self._reply(503, {"success": False, "error": "busy"})
            if body.get("action") == "describe":
                return self._reply(200, {"success": True, "result": json.dumps({"rows": [{"name": "facts"}]})})
            if "FAIL" in body["sql"]:
                return self._reply(200, {"success": False, "error": "no such table: nope"})
            offset, size = body.get("offset", 0), body["max_rows"]
            page = ROWS[offset:offset + size]
            result = {"columns": ["id", "label"], "rows": page}
            if offset + size < len(ROWS):
                result["truncated"] = True
                result["next_offset"] = offset if state["bad_paging"] else offset + len(page)
            return self._reply(200, {"success": True, "result": json.dumps(result)})
        if tool == "mutate_workflow_db":
            if "statements" in body:
                results = [{"rows_affected": 1, "last_insert_id": 7} for _ in body["statements"]]
                return self._reply(200, {"success": True, "result": json.dumps({"results": results, "total_rows_affected": len(results)})})
            if body["sql"].startswith("CREATE"):
                return self._reply(200, {"success": False, "error": "schema changes are not accepted here"})
            n = len(body.get("param_sets") or [None])
            return self._reply(200, {"success": True, "result": json.dumps({"results": [{"rows_affected": n, "last_insert_id": 42}], "total_rows_affected": n})})
        self._reply(404, {"success": False, "error": "unknown tool"})

server = HTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
os.environ["MCP_CUSTOM"] = "http://127.0.0.1:%d/tools/custom" % server.server_port
os.environ["MCP_AUTH"] = "Authorization: Bearer tok"
# the fake mounts every tool directly under the path it was given
Handler.path_prefix = "/tools/custom/"
orig_post = Handler.do_POST
def prefixed(self):
    self.path = self.path.replace("/tools/custom/", "/")
    orig_post(self)
Handler.do_POST = prefixed

from agentworks_db import (DBError, describe, execute, execute_many, insert, iter_query,
                           query, query_one, scalar, transaction)

def must_raise(fn, text=""):
    try:
        fn()
    except DBError as error:
        assert text in str(error), (text, str(error))
    else:
        raise AssertionError("no DBError for %s" % text)

# reads: every page, once, in order; auth header sent
rows = query("SELECT id, label FROM facts ORDER BY id")
assert len(rows) == 1234 and [r["id"] for r in rows] == list(range(1234))
assert len([c for c in calls if c[0] == "query_workflow_db"]) == 1  # 1,234 rows fit one 5,000-row page
assert all(c[2] == "Bearer tok" for c in calls)
calls.clear()
paged = list(iter_query("SELECT id FROM facts ORDER BY id", page_size=500))
assert len(paged) == 1234 and len({r["id"] for r in paged}) == 1234
assert [c[1].get("offset", 0) for c in calls] == [0, 500, 1000]
assert all(c[1]["max_rows"] == 500 for c in calls)
calls.clear()
assert len(query("SELECT 1", limit=10)) == 10
assert calls[0][1]["max_rows"] == 10 and len(calls) == 1
assert query_one("SELECT 1")["id"] == 0
assert scalar("SELECT 1") == 0
assert describe("facts") == [{"name": "facts"}]
calls.clear()
query("SELECT ?, ?", [1, "a"], limit=1)
assert calls[0][1]["params"] == [1, "a"]
must_raise(lambda: query("FAIL"), "no such table")
state["bad_paging"] = True
must_raise(lambda: list(iter_query("SELECT 1", page_size=500)), "paging did not advance")
state["bad_paging"] = False

# a read retries a transient server error; a write never does
state["fail_query_once"] = True
calls.clear()
assert len(query("SELECT 1", limit=1)) == 1 and len(calls) == 2

# writes
calls.clear()
assert execute("UPDATE t SET a = ?", [1]) == 1
assert calls[0][1] == {"sql": "UPDATE t SET a = ?", "params": [1]}
assert insert("INSERT INTO t VALUES (?)", [1]) == 42
calls.clear()
assert execute_many("INSERT INTO t VALUES (?)", [[i] for i in range(2500)], chunk=1000) == 2500
assert [len(c[1]["param_sets"]) for c in calls] == [1000, 1000, 500]
must_raise(lambda: execute_many("INSERT INTO t VALUES (?)", [[i] for i in range(5001)], atomic=True), "at most 5000")
must_raise(lambda: execute_many("INSERT INTO t VALUES (?)", [[1]], chunk=0), "chunk")
assert transaction([("INSERT INTO t VALUES (?)", [1]), ("DELETE FROM t", None)]) == [1, 1]
must_raise(lambda: transaction([]), "1 to 200")
must_raise(lambda: transaction([("DELETE FROM t", None)] * 201), "1 to 200")
must_raise(lambda: execute("CREATE TABLE x (a)"), "schema changes")

# values: dates and decimals go out as text; bytes are refused; NaN is refused
calls.clear()
execute("INSERT INTO t VALUES (?, ?, ?)", [datetime.date(2026, 10, 4), decimal.Decimal("1.50"), datetime.datetime(2026, 10, 4, 1, 2, 3)])
assert calls[0][1]["params"] == ["2026-10-04", "1.50", "2026-10-04T01:02:03"]
must_raise(lambda: execute("INSERT INTO t VALUES (?)", [b"x"]), "BLOB")
must_raise(lambda: execute("INSERT INTO t VALUES (?)", [float("nan")]), "JSON")

# outside a step: no environment, a clear refusal
bridge = dict(os.environ)
for key in ("MCP_CUSTOM", "MCP_AUTH"):
    del os.environ[key]
must_raise(lambda: query("SELECT 1"), "workflow step")
os.environ.update({"MCP_CUSTOM": bridge["MCP_CUSTOM"]})
must_raise(lambda: query("SELECT 1"), "MCP_AUTH")
os.environ["MCP_AUTH"] = "Authorization: Bearer wrong"
must_raise(lambda: query("SELECT 1"), "unauthorized")
print("ok")
`
	cmd := exec.Command(python, "-B", "-c", script)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("database helper: %v\n%s", err, output)
	}
}
