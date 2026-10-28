#!/usr/bin/env python3
"""PLAT-825-139: mechanical saved-source conversion, with the agent stopped.

--check reads only. Apply backs up each changed file. Unknown call shapes are
reported and left intact. Published dashboards use the existing workspace API
to create revisions; immutable snapshots and history are never edited.
"""
import argparse
import ast
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import urllib.request

DROPPED_TABLES = ('goal_lead_builder_asks', 'goal_lead_conversation', 'goal_lead_messages', 'goal_lead_qa_requests', 'pulse_agent_metrics', 'pulse_consolidation', 'pulse_context_records', 'pulse_decisions', 'pulse_fast_request', 'pulse_final_command_state', 'pulse_finding_details', 'pulse_finding_events', 'pulse_finding_issue_aliases', 'pulse_fix_attempt_findings', 'pulse_fix_attempts', 'pulse_fix_verifications', 'pulse_goal_check_runs', 'pulse_goal_checks', 'pulse_goal_work', 'pulse_impact_assessments', 'pulse_intervention_effects', 'pulse_intervention_sources', 'pulse_interventions', 'pulse_issues', 'pulse_module_audit', 'pulse_module_result_history', 'pulse_module_state', 'pulse_prompt_budget_metrics', 'pulse_recommendations', 'pulse_review_focus_history', 'pulse_review_focus_state', 'pulse_review_log', 'pulse_review_notes', 'pulse_review_recovery', 'pulse_reviews', 'pulse_run_mode', 'pulse_schedule_state', 'pulse_schema_migrations', 'pulse_shadow_signal_observation')
TABLE = re.compile(r"\bpulse_goal_observations\b", re.I)
SKIP = {".git", "node_modules", "__pycache__", ".venv", "venv", ".sandbox-cache",
        ".cache", ".next", "history", "archive", "archived", "backup", "backups",
        "runs", "migration-backups", ".claude", ".cursor", ".codex", ".agents"}
EXTENSIONS = {".py", ".js", ".mjs", ".ts", ".tsx", ".html", ".sql", ".md", ".sh", ".json"}


def catalog(directory):
    choices = collections.defaultdict(set)
    families = set()
    actions = []
    for path in sorted(directory.glob("*-actions.json")):
        actions.extend(json.loads(path.read_text()))
    if not actions:
        raise ValueError("catalog directory contains no action definitions")
    selected = collections.defaultdict(dict)
    for action in actions:
        families.add(action["family"])
        value = (action["family"], action["name"], action.get("scope", ""))
        for name in [action.get("member"), action.get("source")] + action.get("aliases", []):
            if name:
                choices[name].add(value)
        if action.get("source") and action.get("source_action"):
            key = (action.get("source_key") or "action", action["source_action"])
            selected[action["source"]].setdefault(key, set()).add(value)
    mapping = {name: next(iter(values)) for name, values in choices.items()
               if name not in families and len(values) == 1}
    # A source tool shared by several actions (brain_read: read/search/diff)
    # is resolved per call from the selector the saved payload already passes.
    for name, keys in selected.items():
        if name in families or name in mapping or len({key for key, _ in keys}) != 1:
            continue
        mapping[name] = {"key": next(iter(keys))[0],
                         "actions": {value: next(iter(targets)) for (_, value), targets in keys.items()
                                     if len(targets) == 1}}
    return mapping, set(choices) - families


def positions(source):
    raw = source.encode()
    lines = raw.splitlines(keepends=True)
    offsets = [0]
    for line in lines:
        offsets.append(offsets[-1] + len(line))
    def span(node):
        return offsets[node.lineno - 1] + node.col_offset, offsets[node.end_lineno - 1] + node.end_col_offset
    def text(node):
        start, end = span(node)
        return raw[start:end].decode()
    return raw, span, text


def payload(source, fields):
    # Keep the original argument expression verbatim and evaluate it once.
    suffix = ", ".join(key + "=" + repr(value) for key, value in fields.items())
    return "dict((" + source + ") or {}, " + suffix + ")"


def python_calls(source, mapping):
    try:
        tree = ast.parse(source)
    except SyntaxError:
        return source, 0, ["python_parse"]
    raw, span, text = positions(source)
    replacements, changed, unsupported = [], 0, []
    parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
    def scope(node):
        while node in parents and not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            node = parents[node]
        return node
    assignments, loads = collections.defaultdict(list), collections.Counter()
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
            assignments[(scope(node), node.targets[0].id)].append(node)
        if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Load):
            loads[(scope(node), node.id)] += 1
    def single_value(node):
        # Resolve only a single-use local assignment. Shared/dynamic URLs or
        # bodies remain reported rather than changing unrelated consumers.
        if isinstance(node, ast.Name):
            key = (scope(node), node.id)
            candidates = assignments[key]
            if len(candidates) == 1 and loads[key] == 1 and candidates[0].lineno < node.lineno:
                return candidates[0].value
        return node
    bridge_context = any(value in source for value in ("MCP_CUSTOM", "/tools/custom/", "/custom/", "MCP_API_URL"))
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call) or not node.args:
            continue
        first = single_value(node.args[0])
        name, url = "", False
        if isinstance(first, ast.Constant) and isinstance(first.value, str):
            if first.value in mapping:
                name = first.value
            else:
                match = re.search(r"/([a-zA-Z_][a-zA-Z_0-9]*)$", first.value)
                if match and match[1] in mapping:
                    name, url = match[1], True
        elif isinstance(first, (ast.JoinedStr, ast.BinOp)):
            match = re.search(r"/([a-zA-Z_][a-zA-Z_0-9]*)[\"']$", text(first))
            if match and match[1] in mapping:
                name, url = match[1], True
        if not name:
            continue
        function = ast.unparse(node.func)
        if not bridge_context and not re.search(r"(?:_tool|custom)", function, re.I):
            continue
        if not url and not re.search(r"(?:post|call|tool|invoke|dispatch)", function, re.I):
            continue
        body = next((kw.value for kw in node.keywords if kw.arg == "json"), None) if url else (node.args[1] if len(node.args) > 1 else None)
        if body is None and url:
            data = next((kw.value for kw in node.keywords if kw.arg == "data"), None)
            if data is not None:
                body = next((call.args[0] for call in ast.walk(single_value(data))
                             if isinstance(call, ast.Call) and call.args and isinstance(call.func, ast.Attribute)
                             and call.func.attr == "dumps"), None)
        if body is None:
            unsupported.append("payload:" + name + ":" + str(node.lineno))
            continue
        target = mapping[name]
        if isinstance(target, dict):
            chosen = None
            if isinstance(body, ast.Dict):
                chosen = next((target["actions"].get(value.value) for key, value in zip(body.keys, body.values)
                               if isinstance(key, ast.Constant) and key.value == target["key"]
                               and isinstance(value, ast.Constant) and isinstance(value.value, str)), None)
            if chosen is None:
                unsupported.append("payload_selector:" + name + ":" + str(node.lineno))
                continue
            target = chosen
        family, action, selector_scope = target
        fields = {"action": action}
        if selector_scope:
            fields["scope"] = selector_scope
        if isinstance(body, ast.Dict) and any(isinstance(key, ast.Constant) and key.value in fields
                and (not isinstance(value, ast.Constant) or value.value != fields[key.value])
                for key, value in zip(body.keys, body.values)):
            unsupported.append("payload_selector_conflict:" + name + ":" + str(node.lineno))
            continue
        original = text(first)
        renamed = original[:original.rfind(name)] + family + original[original.rfind(name) + len(name):]
        replacements.extend([(span(first), renamed), (span(body), payload(text(body), fields))])
        changed += 1
    # Nested/overlapping call expressions are not an exact mechanical match.
    ordered = sorted(replacements)
    if any(ordered[i][0][1] > ordered[i + 1][0][0] for i in range(len(ordered) - 1)):
        return source, 0, unsupported + ["overlapping_calls"]
    for (start, end), value in reversed(ordered):
        raw = raw[:start] + value.encode() + raw[end:]
    result = raw.decode()
    try:
        ast.parse(result)
    except SyntaxError:
        return source, 0, unsupported + ["rewritten_python_parse"]
    return result, changed, unsupported


def shell_calls(source, mapping):
    # Only a single literal endpoint paired with a literal JSON body. Variable
    # bodies, several endpoints and documentation-only URLs remain reported.
    endpoints = list(re.finditer(r"(?P<prefix>(?:/custom/|MCP_CUSTOM[}\"']?/))(?P<name>[a-zA-Z_][a-zA-Z_0-9]*)", source))
    endpoints = [m for m in endpoints if isinstance(mapping.get(m["name"]), tuple)]
    data = list(re.finditer(r"(?:--json|--data(?:-raw)?|-d)\s+(['\"])(\{[^\n]*?\})\1", source))
    if not endpoints:
        return source, 0
    if len(endpoints) != 1 or len(data) != 1:
        return source, 0
    try:
        value = json.loads(data[0][2])
    except ValueError:
        return source, 0
    family, action, scope = mapping[endpoints[0]["name"]]
    fields = {"action": action}
    if scope:
        fields["scope"] = scope
    if not isinstance(value, dict) or any(k in value and value[k] != v for k, v in fields.items()):
        return source, 0
    additions = {k: v for k, v in fields.items() if k not in value}
    original = data[0][2]
    new = original[:-1] + (", " if value else "") + json.dumps(additions)[1:-1] + "}"
    edits = [(endpoints[0].span("name"), family), (data[0].span(2), new)]
    for (start, end), replacement in sorted(edits, reverse=True):
        source = source[:start] + replacement + source[end:]
    return source, 1


def schedule_readers(source):
    try:
        tree = ast.parse(source)
    except SyntaxError:
        return source, 0
    raw, span, text = positions(source)
    parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
    def scope(node):
        while node in parents and not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            node = parents[node]
        return node
    assignments = collections.defaultdict(list)
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
            assignments[(scope(node), node.targets[0].id)].append(node)
    edits = []
    for node in ast.walk(tree):
        if not (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr == "get"
                and node.args and isinstance(node.args[0], ast.Constant) and node.args[0].value == "schedules"):
            continue
        receiver = node.func.value
        loader = receiver
        if isinstance(receiver, ast.Name):
            candidates = assignments.get((scope(node), receiver.id), [])
            if len(candidates) != 1 or candidates[0].lineno >= node.lineno:
                continue
            loader = candidates[0].value
        if not isinstance(loader, ast.Call):
            continue
        expression = text(loader)
        if "workflow.json" not in expression:
            continue
        # A direct expression is safe to repeat for schedules alone; other
        # reads of the original manifest variable remain byte-for-byte intact.
        rewritten = re.sub(r"(?<=[\"'])workflow\.json(?=[\"'])", "schedules.json", expression)
        if rewritten == expression:
            continue
        edits.append((span(receiver), rewritten))
    for (start, end), value in sorted(edits, reverse=True):
        raw = raw[:start] + value.encode() + raw[end:]
    result = raw.decode()
    try:
        ast.parse(result)
    except SyntaxError:
        return source, 0
    return result, len(edits)


def transform(source, extension, mapping):
    counts = {"table": len(TABLE.findall(source)), "tool_calls": 0, "schedule_readers": 0}
    source = TABLE.sub("goal_observations", source)
    unsupported = []
    if extension == ".py":
        source, counts["tool_calls"], unsupported = python_calls(source, mapping)
        source, counts["schedule_readers"] = schedule_readers(source)
    elif extension == ".md":
        def fence(match):
            nonlocal counts, unsupported
            language, body = match["lang"], match["body"]
            if language in ("python", "py"):
                body, number, errors = python_calls(body, mapping)
                unsupported += errors
            else:
                body, number = shell_calls(body, mapping)
            counts["tool_calls"] += number
            return match["prefix"] + body + match["suffix"]
        source = re.sub(r"(?ms)(?P<prefix>^```(?P<lang>python|py|bash|sh|shell)[ \t]*\n)(?P<body>.*?)(?P<suffix>^```[ \t]*$)", fence, source)
    elif extension == ".sh":
        source, counts["tool_calls"] = shell_calls(source, mapping)
    return source, counts, unsupported


def project_roots(root):
    for parent in [root] + list((root / "_users").glob("*")):
        for kind in ("Workflow", "Crew", "Code", "Relay", "Relays"):
            for project in (parent / kind).glob("*"):
                if project.is_dir() and not project.is_symlink():
                    yield project


def active_files(project):
    for name in ("code", "skills", "db/reports"):
        base = project / name
        if any(part.is_symlink() for part in [base] + list(base.parents) if part != project.parent):
            continue
        for parent, directories, files in os.walk(base, followlinks=False):
            directories[:] = [d for d in directories if d not in SKIP and not d.startswith(".")
                              and not (Path(parent) / d).is_symlink()
                              and not (d == "managed" and str(Path(parent).relative_to(project)) in ("db/reports", "code/reports"))]
            for filename in files:
                path = Path(parent) / filename
                if path.is_symlink() or path.suffix not in EXTENSIONS:
                    continue
                # Saved tool transcripts, authoring requests and execution
                # metadata are evidence, not executable source.
                if name == "code" and path.suffix == ".json" and filename != "queries.json":
                    continue
                yield path


def backup(path, root, destination):
    target = destination / path.relative_to(root)
    target.parent.mkdir(parents=True, exist_ok=True)
    if not target.exists():
        shutil.copy2(path, target)


def dashboard_api(args, request):
    token = os.environ.get("WORKSPACE_API_TOKEN", "")
    url = args.workspace_api_url or os.environ.get("WORKSPACE_API_URL", "")
    if (not token or not url) and args.workspace_env_file:
        for line in args.workspace_env_file.read_text().splitlines():
            key, separator, value = line.partition("=")
            if separator and key.strip() == "WORKSPACE_API_TOKEN":
                token = token or value.strip().strip("\"'")
            if separator and key.strip() == "WORKSPACE_API_URL":
                url = url or value.strip().strip("\"'")
    if not url:
        raise ValueError("workspace API URL is required for native revision updates")
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    call = urllib.request.Request(url.rstrip("/") + "/api/dashboards",
                                  data=json.dumps(request).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(call, timeout=90) as response:
        return json.load(response)


def dashboards(project, root, args, mapping, result):
    for state_path in (project / "db/reports/.dashboard-state").glob("*/state.json"):
        if state_path.is_symlink() or state_path.parent.is_symlink():
            continue
        state = json.loads(state_path.read_text())
        if not isinstance(state, dict) or not isinstance(state.get("draft"), str):
            result["unsupported"].append({"path": str(state_path.relative_to(root)), "reason": "unknown_dashboard_state"})
            continue
        originals, changed = {}, {}
        for rev in set(filter(None, [state.get("draft"), state.get("published")])):
            snap_path = state_path.with_name(rev + ".json")
            if snap_path.is_symlink():
                continue
            snap = json.loads(snap_path.read_text())
            originals[rev] = snap
            files = dict(snap["files"])
            for filename, content in files.items():
                if Path(filename).suffix in EXTENSIONS:
                    files[filename], _, _ = transform(content, Path(filename).suffix, mapping)
            if files != snap["files"]:
                changed[rev] = files
        if not changed:
            continue
        if state.get("published") in changed and state.get("draft") != state.get("published"):
            result["unsupported"].append({"path": str(state_path.relative_to(root)),
                                            "reason": "independent_draft_and_published_revisions"})
            continue
        result["dashboards"].append({"state": str(state_path.relative_to(root)), "revisions": sorted(changed)})
        if args.check:
            continue
        if not (args.workspace_api_url or os.environ.get("WORKSPACE_API_URL") or args.workspace_env_file):
            result["unsupported"].append({"path": str(state_path.relative_to(root)), "reason": "workspace_api_required_for_native_revision"})
            continue
        backup(state_path, root, args.backup_dir)
        for rev in originals:
            backup(state_path.with_name(rev + ".json"), root, args.backup_dir)
        request = {"root": str(project.relative_to(root)), "id": state["id"], "include_draft": True}
        rev = state["draft"]
        snap = originals[rev]
        response = dashboard_api(args, {**request, "action": "update", "expected_revision": rev,
                                        "title": snap["title"], "motion": snap.get("motion", False),
                                        "files": changed[rev]})
        if rev == state.get("published"):
            dashboard_api(args, {**request, "action": "publish", "expected_revision": response["dashboard"]["revision"]})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-dir", required=True, type=Path)
    parser.add_argument("--backup-dir", type=Path)
    parser.add_argument("--catalog-dir", required=True, type=Path)
    parser.add_argument("--workspace-api-url", default="")
    parser.add_argument("--workspace-env-file", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    if not args.check and not args.backup_dir:
        parser.error("--backup-dir is required when applying")
    root = args.workspace_dir.resolve()
    mapping, retired = catalog(args.catalog_dir)
    result = {"event": "saved_platform_reference_conversion", "check": args.check, "files": [],
              "dashboards": [], "unsupported": [], "platform_helper_imports": []}
    for project in project_roots(root):
        for path in active_files(project):
            rel = str(path.relative_to(root))
            try:
                with path.open(encoding="utf-8", newline="") as stream:
                    before = stream.read()
            except (OSError, UnicodeError):
                result["unsupported"].append({"path": rel, "reason": "unreadable_utf8"})
                continue
            if path.name == "agentworks_db.py":
                result["unsupported"].append({"path": rel, "reason": "vendored_helper_copy_needs_owner_review"})
                continue
            if re.search(r"\b(?:import agentworks_db|from agentworks_db import)\b", before):
                result["platform_helper_imports"].append(rel)
            after, counts, errors = transform(before, path.suffix, mapping)
            remaining = sorted({word for word in re.findall(r"\b[A-Za-z_][A-Za-z_0-9]*\b", after)
                                if word in retired and word not in {"users", "slack", "image_gen"}})
            leftover_tables = sorted(set(re.findall(r"\b[A-Za-z_][A-Za-z_0-9]*\b", after)).intersection(DROPPED_TABLES))
            if leftover_tables:
                errors.append("dropped_tables:" + ",".join(leftover_tables))
            if not counts["schedule_readers"] and "workflow.json" in after and re.search(r"\.get\(\s*[\"']schedules[\"']", after):
                errors.append("unresolved_manifest_schedule_reader_candidate")
            if (errors or remaining) and (errors or path.suffix == ".md" or any(k in after for k in ("MCP_CUSTOM", "/custom/", "MCP_API_URL"))):
                result["unsupported"].append({"path": rel, "reasons": errors, "remaining_literals": remaining})
            if after == before:
                continue
            result["files"].append({"path": rel, "changes": counts,
                                     "before_sha256": hashlib.sha256(before.encode()).hexdigest(),
                                     "after_sha256": hashlib.sha256(after.encode()).hexdigest()})
            if not args.check:
                backup(path, root, args.backup_dir)
                with path.open("w", encoding="utf-8", newline="") as stream:
                    stream.write(after)
        try:
            dashboards(project, root, args, mapping, result)
        except (OSError, ValueError, KeyError, TypeError) as exc:
            result["unsupported"].append({"path": str(project.relative_to(root)), "reason": "dashboard_" + type(exc).__name__})
    result["changed_files"] = len(result["files"])
    result["changed_dashboards"] = len(result["dashboards"])
    result["backup_directory"] = str(args.backup_dir) if args.backup_dir else None
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
