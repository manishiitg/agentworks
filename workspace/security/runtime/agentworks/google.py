"""Google apps use this automation's existing account and service grants.

JSON methods return gog's parsed JSON unchanged. They do not guess a rows/count
schema. Read a smaller range when a response exceeds the service's output limit.
"""
import json
from pathlib import Path
from typing import Sequence
from uuid import uuid4

from . import JSONValue, run_folder
from ._apps import request


def _google(args, connection_id=None, *, json_result=True):
    parameters = {"args": args}
    if connection_id is not None:
        parameters["connection_id"] = connection_id
    return request("google", parameters, json_result=json_result)


class _Sheets:
    @staticmethod
    def read(id: str, range: str, *, connection_id: str | None = None) -> JSONValue:
        return _google(["sheets", "get", id, range, "--json", "--results-only"], connection_id)

    @staticmethod
    def write(id: str, range: str, rows: Sequence[Sequence[JSONValue]], *, connection_id: str | None = None) -> JSONValue:
        return _google(["sheets", "update", id, range, "--values-json", json.dumps(rows, allow_nan=False),
                        "--input", "USER_ENTERED", "--json", "--results-only"], connection_id)

    @staticmethod
    def append(id: str, range: str, rows: Sequence[Sequence[JSONValue]], *, connection_id: str | None = None) -> JSONValue:
        return _google(["sheets", "append", id, range, "--values-json", json.dumps(rows, allow_nan=False),
                        "--input", "USER_ENTERED", "--json", "--results-only"], connection_id)


class _Gmail:
    @staticmethod
    def search(query: str, *, max: int = 20, connection_id: str | None = None) -> JSONValue:
        return _google(["gmail", "search", query, "--max", str(max), "--json", "--results-only"], connection_id)


class _Calendar:
    @staticmethod
    def list(start: str, end: str, *, calendar: str = "primary", connection_id: str | None = None) -> JSONValue:
        return _google(["calendar", "events", calendar, "--from", start, "--to", end, "--json", "--results-only"], connection_id)

    @staticmethod
    def create(title: str, start: str, end: str, *, attendees: Sequence[str] = (),
               calendar: str = "primary", connection_id: str | None = None) -> JSONValue:
        args = ["calendar", "create", calendar, "--summary", title, "--from", start, "--to", end]
        if attendees:
            args += ["--attendees", ",".join(attendees)]
        return _google(args + ["--json", "--results-only"], connection_id)


class _Drive:
    @staticmethod
    def read(doc_id: str, *, connection_id: str | None = None) -> Path:
        """Read a Google Doc's text into this run; no upload or binary download."""
        text = _google(["docs", "cat", doc_id], connection_id, json_result=False)
        file = run_folder / ("google-doc-" + uuid4().hex + ".txt")
        file.write_text(text, encoding="utf-8")
        return file


sheets, gmail, calendar, drive = _Sheets(), _Gmail(), _Calendar(), _Drive()
