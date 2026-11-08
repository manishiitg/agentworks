"""GitHub uses the same token selection as this automation's shell.

JSON methods return the server's existing receipt unchanged. read and diff
return text; a bounded diff retains the server's explicit cutoff notice.
"""
from . import JSONValue
from ._apps import request


def files(repo: str, path: str = "", *, ref: str = "") -> JSONValue:
    return request("github", {"action": "files", "repo": repo, "path": path, "ref": ref})


def read(repo: str, path: str, *, ref: str = "") -> str:
    return request("github", {"action": "read", "repo": repo, "path": path, "ref": ref}, json_result=False)


def issues(repo: str, *, state: str = "open") -> JSONValue:
    return request("github", {"action": "issues", "repo": repo, "state": state})


def create_issue(repo: str, title: str, body: str = "") -> JSONValue:
    return request("github", {"action": "issue_create", "repo": repo, "title": title, "body": body})


def comment(repo: str, number: int, body: str) -> JSONValue:
    return request("github", {"action": "comment", "repo": repo, "number": number, "body": body})


def prs(repo: str, *, state: str = "open") -> JSONValue:
    return request("github", {"action": "prs", "repo": repo, "state": state})


def create_pr(repo: str, head: str, base: str, title: str, body: str = "") -> JSONValue:
    return request("github", {"action": "pr_create", "repo": repo, "head": head, "base": base, "title": title, "body": body})


def diff(repo: str, number: int) -> str:
    return request("github", {"action": "pr_diff", "repo": repo, "number": number}, json_result=False)
