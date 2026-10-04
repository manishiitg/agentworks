"""Managed workflow database access for sandboxed Python steps.

    from agentworks_db import query, query_one, scalar, iter_query
    from agentworks_db import execute, execute_many, transaction

The same managed layer the agent tools use (query_workflow_db and
mutate_workflow_db), reached through the step's own bridge session: SQL is
checked, every write is one transaction, and nothing opens db.sqlite directly.
Use ``?`` placeholders and pass values in ``params``.

Reads
    query(sql, params=None, limit=None)  -> list of dict rows (every page)
    iter_query(sql, params=None)         -> iterator of dict rows (page by page)
    query_one(sql, params=None)          -> first row as a dict, or None
    scalar(sql, params=None)             -> first column of the first row, or None
    describe(table=None)                 -> schema rows
Give a big SELECT an ORDER BY so pages stay stable.

Writes (INSERT, UPDATE, DELETE only; schema changes are migrations)
    execute(sql, params=None)            -> rows affected
    insert(sql, params=None)             -> last insert id
    execute_many(sql, rows, chunk=2000)  -> rows affected over every row
    transaction([(sql, params), ...])    -> rows affected per statement

execute_many sends ``chunk`` rows per call; each call is atomic and a failing
call raises before later chunks are sent. Pass atomic=True to require the
whole list in one transaction (at most 5000 rows). Every failure raises DBError.
"""

import datetime
import decimal
import json
import os
import time
import urllib.error
import urllib.request

PAGE_ROWS = 5000
MAX_STATEMENTS = 200
MAX_EXECUTIONS = 5000
TIMEOUT_SECONDS = 120

__all__ = [
    "DBError", "query", "iter_query", "query_one", "scalar", "describe",
    "execute", "insert", "execute_many", "transaction",
]


class DBError(RuntimeError):
    """A database call failed; the message is the platform's own."""


def _default(value):
    if isinstance(value, (datetime.datetime, datetime.date, datetime.time)):
        return value.isoformat()
    if isinstance(value, decimal.Decimal):
        return str(value)
    if isinstance(value, (bytes, bytearray, memoryview)):
        raise DBError("BLOB values are not supported; store text (for example base64) instead")
    raise TypeError("value of type %s is not JSON serializable" % type(value).__name__)


def _auth_header():
    raw = os.environ.get("MCP_AUTH", "")
    name, separator, value = raw.partition(":")
    if not separator or not name.strip() or not value.strip():
        raise DBError("MCP_AUTH is not set: agentworks_db only works inside a workflow step")
    return name.strip(), value.strip()


def _call(tool, arguments, retries=0):
    base = os.environ.get("MCP_CUSTOM", "").rstrip("/")
    if not base:
        raise DBError("MCP_CUSTOM is not set: agentworks_db only works inside a workflow step")
    header_name, header_value = _auth_header()
    try:
        payload = json.dumps(arguments, ensure_ascii=False, allow_nan=False, default=_default).encode("utf-8")
    except (TypeError, ValueError) as error:
        raise DBError("a value cannot be sent as JSON: %s" % error)
    request = urllib.request.Request(
        base + "/" + tool, data=payload, method="POST",
        headers={"Content-Type": "application/json", header_name: header_value},
    )
    attempt = 0
    while True:
        try:
            with urllib.request.urlopen(request, timeout=TIMEOUT_SECONDS) as response:
                body = response.read().decode("utf-8")
            break
        except urllib.error.HTTPError as error:
            body = error.read().decode("utf-8", "replace")
            detail = body
            try:
                detail = json.loads(body).get("error") or body
            except ValueError:
                pass
            if error.code >= 500 and attempt < retries:
                attempt += 1
                time.sleep(0.5 * attempt)
                continue
            raise DBError("%s failed (HTTP %d): %s" % (tool, error.code, str(detail)[:2000]))
        except (urllib.error.URLError, OSError) as error:
            if attempt < retries:
                attempt += 1
                time.sleep(0.5 * attempt)
                continue
            raise DBError("%s could not be reached: %s" % (tool, error))
    try:
        outer = json.loads(body)
    except ValueError:
        raise DBError("%s returned something that is not JSON: %s" % (tool, body[:200]))
    if not outer.get("success"):
        raise DBError(str(outer.get("error") or outer.get("message") or "%s failed" % tool)[:2000])
    try:
        return json.loads(outer.get("result") or "null")
    except ValueError:
        raise DBError("%s returned an unreadable result: %s" % (tool, str(outer.get("result"))[:200]))


def _read_arguments(sql, params, max_rows, offset):
    arguments = {"sql": sql, "max_rows": max_rows}
    if params:
        arguments["params"] = list(params)
    if offset:
        arguments["offset"] = offset
    return arguments


def iter_query(sql, params=None, page_size=PAGE_ROWS):
    """Yield every row of a SELECT as a dict, one page of ``page_size`` at a time."""
    page_size = max(1, min(int(page_size), 10000))
    offset = 0
    while True:
        result = _call("query_workflow_db", _read_arguments(sql, params, page_size, offset), retries=2) or {}
        for row in result.get("rows") or []:
            yield row
        if not result.get("truncated"):
            return
        following = result.get("next_offset") or 0
        if following <= offset:
            raise DBError("paging did not advance; give the SELECT an ORDER BY and try again")
        offset = following


def query(sql, params=None, limit=None):
    """All rows of a SELECT as a list of dicts (at most ``limit`` when given)."""
    rows = []
    page_size = PAGE_ROWS if limit is None else min(PAGE_ROWS, max(1, int(limit)))
    for row in iter_query(sql, params, page_size=page_size):
        rows.append(row)
        if limit is not None and len(rows) >= limit:
            break
    return rows


def query_one(sql, params=None):
    """The first row as a dict, or None when the SELECT returns nothing."""
    rows = query(sql, params, limit=1)
    return rows[0] if rows else None


def scalar(sql, params=None):
    """The first column of the first row, or None."""
    row = query_one(sql, params)
    return next(iter(row.values())) if row else None


def describe(table=None):
    """Schema rows for one table, or for every table and view."""
    arguments = {"action": "describe"}
    if table:
        arguments["table"] = table
    result = _call("query_workflow_db", arguments, retries=2) or {}
    return result.get("rows") or []


def _mutate(arguments):
    result = _call("mutate_workflow_db", arguments)
    if not isinstance(result, dict) or "results" not in result:
        raise DBError("mutate_workflow_db returned an unexpected result")
    return result


def execute(sql, params=None):
    """Run one INSERT, UPDATE or DELETE; return the rows affected."""
    arguments = {"sql": sql}
    if params:
        arguments["params"] = list(params)
    return _mutate(arguments)["results"][0].get("rows_affected", 0)


def insert(sql, params=None):
    """Run one INSERT; return the last insert id."""
    arguments = {"sql": sql}
    if params:
        arguments["params"] = list(params)
    return _mutate(arguments)["results"][0].get("last_insert_id", 0)


def execute_many(sql, rows, chunk=2000, atomic=False):
    """Run one statement once per row of ``rows``; return the total rows affected."""
    rows = [list(row) for row in rows]
    chunk = int(chunk)
    if chunk < 1 or chunk > MAX_EXECUTIONS:
        raise DBError("chunk must be between 1 and %d" % MAX_EXECUTIONS)
    if atomic and len(rows) > MAX_EXECUTIONS:
        raise DBError("atomic execute_many takes at most %d rows (got %d); split the work or drop atomic" % (MAX_EXECUTIONS, len(rows)))
    total = 0
    for start in range(0, len(rows), chunk):
        result = _mutate({"sql": sql, "param_sets": rows[start:start + chunk]})
        total += result.get("total_rows_affected", 0)
    return total


def transaction(statements):
    """Run several statements as one all-or-nothing batch.

    ``statements`` is a list of ``(sql, params)`` pairs (``params`` may be None);
    returns the rows affected by each.
    """
    prepared = []
    for item in statements:
        sql, params = item if isinstance(item, (tuple, list)) else (item, None)
        statement = {"sql": sql}
        if params:
            statement["params"] = list(params)
        prepared.append(statement)
    if not prepared or len(prepared) > MAX_STATEMENTS:
        raise DBError("a transaction takes 1 to %d statements (got %d)" % (MAX_STATEMENTS, len(prepared)))
    result = _mutate({"statements": prepared})
    return [entry.get("rows_affected", 0) for entry in result["results"]]
