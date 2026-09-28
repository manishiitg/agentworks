#!/usr/bin/env python3
"""Validate AgentWorks playbook packages against Playbook Specification v1."""

from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REQUIRED_SECTIONS = [
    "Outcome",
    "When to use",
    "Discovery and user direction",
    "Required inputs",
    "Plan and AgentWorks tools",
    "Knowledge and persistence",
    "Validation and reporting",
    "Guardrails",
    "Read details when needed",
    "Completion contract",
]
REQUIRED_MANIFEST_FIELDS = [
    "metadata_version",
    "content_schema",
    "id",
    "version",
    "title",
    "description",
    "hierarchy",
    "order",
    "entrypoint",
    "audience",
    "team_scope",
    "setup_prompt",
    "setup_inputs",
    "required_capabilities",
    "recommended_tools",
    "pulse_focus",
    "outputs",
]
REQUIRED_TOOL_FIELDS = {"id", "name", "type", "purpose", "capability", "optional"}
REQUIRED_PULSE_FOCUS_FIELDS = {"module", "label", "focus_areas", "review_when"}
PULSE_FOCUS_MODULES = {"strategic_review"}
REQUIRED_SKILL_RECOMMENDATION_FIELDS = {
    "id",
    "name",
    "publisher",
    "source",
    "install_hint",
    "purpose",
    "optional",
}
KEBAB_CASE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
SEMVER = re.compile(r"^\d+\.\d+\.\d+$")
ARTIFACT_TYPE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*/v\d+$")
MARKDOWN_LINK = re.compile(r"\[[^\]]+\]\(([^)]+)\)")


def fail(errors: list[str], path: Path, message: str) -> None:
    errors.append(f"{path.relative_to(ROOT.parent)}: {message}")


def frontmatter(text: str) -> dict[str, str]:
    match = re.match(r"\A---\s*\n(.*?)\n---\s*\n", text, re.DOTALL)
    if not match:
        return {}
    values: dict[str, str] = {}
    for line in match.group(1).splitlines():
        if ":" in line:
            key, value = line.split(":", 1)
            values[key.strip()] = value.strip()
    return values


def word_count_without_frontmatter(text: str) -> int:
    body = re.sub(r"\A---\s*\n.*?\n---\s*\n", "", text, count=1, flags=re.DOTALL)
    return len(re.findall(r"\b[\w'-]+\b", body))


def heading_slug(heading: str) -> str:
    value = re.sub(r"[^a-z0-9 _-]", "", heading.strip().lower())
    return re.sub(r"[ _]+", "-", value)


def markdown_anchors(path: Path) -> set[str]:
    return {
        heading_slug(heading)
        for heading in re.findall(r"^#{1,6}\s+(.+?)\s*$", path.read_text(), re.MULTILINE)
    }


def validate_links(path: Path, text: str, errors: list[str]) -> None:
    for raw_target in MARKDOWN_LINK.findall(text):
        target = raw_target.strip()
        if not target or "://" in target:
            continue
        relative, _, anchor = target.partition("#")
        linked_path = path if not relative else path.parent / relative
        if not linked_path.exists():
            fail(errors, path, f"broken local link: {target}")
        elif anchor and linked_path.suffix.lower() == ".md" and anchor not in markdown_anchors(linked_path):
            fail(errors, path, f"broken local anchor: {target}")


def validate_package(package: Path, errors: list[str]) -> dict[str, object] | None:
    manifest_path = package / "playbook.json"
    try:
        manifest = json.loads(manifest_path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        fail(errors, manifest_path, f"invalid JSON: {exc}")
        return None

    for field in REQUIRED_MANIFEST_FIELDS:
        if field not in manifest:
            fail(errors, manifest_path, f"missing required field {field!r}")

    playbook_id = manifest.get("id")
    if not isinstance(playbook_id, str) or not KEBAB_CASE.fullmatch(playbook_id):
        fail(errors, manifest_path, "id must be lowercase kebab-case")
    if package.name != playbook_id:
        fail(errors, manifest_path, "id must match the package directory")
    if manifest.get("metadata_version") != 1:
        fail(errors, manifest_path, "metadata_version must be 1")
    if manifest.get("content_schema") != "agentworks-playbook/v1":
        fail(errors, manifest_path, "content_schema must be agentworks-playbook/v1")
    if not isinstance(manifest.get("version"), str) or not SEMVER.fullmatch(manifest["version"]):
        fail(errors, manifest_path, "version must use MAJOR.MINOR.PATCH")
    changelog = manifest.get("changelog", [])
    if not isinstance(changelog, list):
        fail(errors, manifest_path, "changelog must be a list")
    else:
        for index, entry in enumerate(changelog):
            if not isinstance(entry, dict) or set(entry) != {"version", "summary"}:
                fail(errors, manifest_path, f"changelog[{index}] needs only version and summary")
                continue
            if not isinstance(entry["version"], str) or not SEMVER.fullmatch(entry["version"]):
                fail(errors, manifest_path, f"changelog[{index}].version must use MAJOR.MINOR.PATCH")
            if not isinstance(entry["summary"], str) or not entry["summary"].strip():
                fail(errors, manifest_path, f"changelog[{index}].summary must be non-empty")
    if manifest.get("entrypoint") != "SKILL.md":
        fail(errors, manifest_path, "entrypoint must be SKILL.md")
    if manifest.get("audience") != "workflow_builder":
        fail(errors, manifest_path, "audience must be workflow_builder")
    if manifest.get("team_scope") != "small_team":
        fail(errors, manifest_path, "team_scope must be small_team for the current catalog")
    hierarchy = manifest.get("hierarchy")
    if not isinstance(hierarchy, list) or not hierarchy or hierarchy[0] != "AgentWorks":
        fail(errors, manifest_path, "hierarchy must be a non-empty list starting with AgentWorks")
    if not isinstance(manifest.get("order"), int) or manifest["order"] < 1:
        fail(errors, manifest_path, "order must be a positive integer")

    for index, setup_input in enumerate(manifest.get("setup_inputs", [])):
        if not isinstance(setup_input, dict) or not {"id", "label", "required"}.issubset(setup_input):
            fail(errors, manifest_path, f"setup_inputs[{index}] needs id, label, and required")
    for index, tool in enumerate(manifest.get("recommended_tools", [])):
        if not isinstance(tool, dict) or not REQUIRED_TOOL_FIELDS.issubset(tool):
            fail(errors, manifest_path, f"recommended_tools[{index}] has an incomplete shape")
        elif tool.get("optional") is not True:
            fail(errors, manifest_path, f"recommended_tools[{index}].optional must be true")
    slots = manifest.get("agent_slots", [])
    if not isinstance(slots, list):
        fail(errors, manifest_path, "agent_slots must be a list")
        slots = []
    slot_outputs: dict[str, str] = {}
    slot_required: dict[str, bool] = {}
    for index, slot in enumerate(slots):
        if not isinstance(slot, dict) or not {"id", "agent_playbook_id", "required", "output"}.issubset(slot):
            fail(errors, manifest_path, f"agent_slots[{index}] is incomplete")
            continue
        slot_id = slot["id"]
        if not isinstance(slot_id, str) or not slot_id or slot_id in slot_outputs:
            fail(errors, manifest_path, f"agent_slots[{index}] has an invalid or duplicate id")
            continue
        if not isinstance(slot.get("agent_playbook_id"), str) or not KEBAB_CASE.fullmatch(slot["agent_playbook_id"]):
            fail(errors, manifest_path, f"agent_slots[{index}] needs a canonical Crew template id")
        if type(slot.get("required")) is not bool:
            fail(errors, manifest_path, f"agent_slots[{index}].required must be a boolean")
        if not isinstance(slot["output"], str) or not ARTIFACT_TYPE.fullmatch(slot["output"]):
            fail(errors, manifest_path, f"agent_slots[{index}] needs a versioned output type")
            continue
        slot_outputs[slot_id] = slot["output"]
        slot_required[slot_id] = slot["required"] is True
    handoffs = manifest.get("handoffs", [])
    if not isinstance(handoffs, list):
        fail(errors, manifest_path, "handoffs must be a list")
        handoffs = []
    handoff_ids: set[str] = set()
    edges: dict[str, list[str]] = {slot_id: [] for slot_id in slot_outputs}
    for index, handoff in enumerate(handoffs):
        if not isinstance(handoff, dict) or not {"id", "from", "to", "artifact_type", "required"}.issubset(handoff):
            fail(errors, manifest_path, f"handoffs[{index}] is incomplete")
            continue
        if not isinstance(handoff["id"], str) or not handoff["id"] or handoff["id"] in handoff_ids:
            fail(errors, manifest_path, f"handoffs[{index}] has an invalid or duplicate id")
            continue
        handoff_ids.add(handoff["id"])
        if type(handoff.get("required")) is not bool:
            fail(errors, manifest_path, f"handoffs[{index}].required must be a boolean")
        source_slot = handoff["from"]
        target_slot = handoff["to"]
        if not isinstance(source_slot, str) or not isinstance(target_slot, str) or source_slot not in slot_outputs or target_slot not in slot_outputs:
            fail(errors, manifest_path, f"handoffs[{index}] references a missing agent slot")
        elif handoff["artifact_type"] != slot_outputs[source_slot]:
            fail(errors, manifest_path, f"handoffs[{index}] artifact_type does not match its producer output")
        if isinstance(source_slot, str) and isinstance(target_slot, str) and source_slot in slot_outputs and target_slot in slot_outputs:
            edges[source_slot].append(target_slot)
            if source_slot == target_slot:
                fail(errors, manifest_path, f"handoffs[{index}] cannot point to the same slot")
            if handoff.get("required") is True and not (slot_required[source_slot] and slot_required[target_slot]):
                fail(errors, manifest_path, f"handoffs[{index}] cannot require an optional slot")
    visiting: set[str] = set()
    visited: set[str] = set()

    def cycle_from(slot_id: str) -> bool:
        if slot_id in visiting:
            return True
        if slot_id in visited:
            return False
        visiting.add(slot_id)
        if any(cycle_from(target) for target in edges[slot_id]):
            return True
        visiting.remove(slot_id)
        visited.add(slot_id)
        return False

    if any(cycle_from(slot_id) for slot_id in edges):
        fail(errors, manifest_path, "agent handoff graph contains a cycle")
    setup_checks = manifest.get("setup_checks", [])
    if len(slots) > 1:
        if not isinstance(setup_checks, list) or not 5 <= len(setup_checks) <= 10:
            fail(errors, manifest_path, "multi-Crew Playbook needs five to ten tracked setup checks")
        negative_examples = [
            example for example in (package / "examples").glob("*.json")
            if any(term in example.stem for term in ("invalid", "rejected", "false"))
        ]
        if not negative_examples:
            fail(errors, manifest_path, "multi-Crew Playbook needs a rejected example artifact")
        if not (package / "scripts" / "test_validate_handoff.py").is_file():
            fail(errors, manifest_path, "multi-Crew Playbook needs an executable handoff contract suite")
    if setup_checks:
        setup_path = package / "SETUP.json"
        try:
            setup = json.loads(setup_path.read_text())
            defined_checks = [check["id"] for check in setup["checks"]]
        except (OSError, json.JSONDecodeError, KeyError, TypeError) as exc:
            fail(errors, manifest_path, f"setup_checks needs a valid SETUP.json: {exc}")
        else:
            if len(setup_checks) != len(set(setup_checks)) or defined_checks != setup_checks:
                fail(errors, manifest_path, "setup_checks must match ordered SETUP.json check IDs")
            if setup.get("playbook_id") != playbook_id or setup.get("playbook_version") != manifest.get("version"):
                fail(errors, manifest_path, "SETUP.json id and version must match playbook.json")
    pulse_focus = manifest.get("pulse_focus", [])
    if not isinstance(pulse_focus, list):
        fail(errors, manifest_path, "pulse_focus must be a list")
    else:
        modules: set[object] = set()
        for index, focus in enumerate(pulse_focus):
            if not isinstance(focus, dict) or not REQUIRED_PULSE_FOCUS_FIELDS.issubset(focus):
                fail(errors, manifest_path, f"pulse_focus[{index}] has an incomplete shape")
                continue
            modules.add(focus.get("module"))
            if not isinstance(focus.get("focus_areas"), list) or not focus["focus_areas"]:
                fail(errors, manifest_path, f"pulse_focus[{index}].focus_areas must be a non-empty list")
            if not isinstance(focus.get("review_when"), list) or not focus["review_when"]:
                fail(errors, manifest_path, f"pulse_focus[{index}].review_when must be a non-empty list")
        if modules != PULSE_FOCUS_MODULES:
            fail(errors, manifest_path, "pulse_focus must define strategic_review only")
    recommended_skills = manifest.get("recommended_skills", [])
    if not isinstance(recommended_skills, list):
        fail(errors, manifest_path, "recommended_skills must be a list")
    else:
        for index, skill in enumerate(recommended_skills):
            if not isinstance(skill, dict) or not REQUIRED_SKILL_RECOMMENDATION_FIELDS.issubset(skill):
                fail(errors, manifest_path, f"recommended_skills[{index}] has an incomplete shape")
            elif skill.get("optional") is not True:
                fail(errors, manifest_path, f"recommended_skills[{index}].optional must be true")

    skill_path = package / str(manifest.get("entrypoint", "SKILL.md"))
    if not skill_path.is_file():
        fail(errors, manifest_path, "entrypoint does not exist")
        return manifest
    text = skill_path.read_text()
    metadata = frontmatter(text)
    if metadata.get("name") != playbook_id:
        fail(errors, skill_path, "frontmatter name must match playbook id")
    if not metadata.get("description"):
        fail(errors, skill_path, "frontmatter description is required")
    headings = re.findall(r"^## (.+?)\s*$", text, re.MULTILINE)
    if headings != REQUIRED_SECTIONS:
        fail(errors, skill_path, f"H2 sections must exactly match the v1 order: {REQUIRED_SECTIONS}")
    section_bodies = {
        heading: body
        for heading, body in re.findall(
            r"^## (.+?)\s*\n(.*?)(?=^## |\Z)", text, re.MULTILINE | re.DOTALL
        )
    }
    design_guidance = section_bodies.get("Read details when needed", "")
    if "workflow-design-and-outcomes.md" not in design_guidance:
        fail(errors, skill_path, "Read details when needed must link the shared workflow design and outcomes guide")
    plan_guidance = section_bodies.get("Plan and AgentWorks tools", "")
    if not re.search(r"\b(?:step|steps|route|routes)\b", plan_guidance, re.IGNORECASE):
        fail(errors, skill_path, "Plan and AgentWorks tools must explain plan steps or routes")
    reporting_guidance = section_bodies.get("Validation and reporting", "")
    if not re.search(r"\bdashboard\b", reporting_guidance, re.IGNORECASE):
        fail(errors, skill_path, "Validation and reporting must describe the reporting dashboard")
    words = word_count_without_frontmatter(text)
    if words > 500:
        fail(errors, skill_path, f"SKILL.md has {words} words; maximum is 500")
    validate_links(skill_path, text, errors)

    for markdown_path in package.rglob("*.md"):
        validate_links(markdown_path, markdown_path.read_text(), errors)
    for json_path in package.rglob("*.json"):
        try:
            json.loads(json_path.read_text())
        except json.JSONDecodeError as exc:
            fail(errors, json_path, f"invalid JSON: {exc}")
    return manifest


def validate_crew_bindings(path: Path, manifest: dict[str, object], crew_ids: set[str], errors: list[str]) -> None:
    slots = manifest.get("agent_slots", [])
    for index, slot in enumerate(slots if isinstance(slots, list) else []):
        agent_id = slot.get("agent_playbook_id") if isinstance(slot, dict) else None
        if isinstance(agent_id, str) and agent_id not in crew_ids:
            fail(errors, path, f"agent_slots[{index}] references an uninstalled Crew template {agent_id!r}")


def main() -> int:
    errors: list[str] = []
    crew_catalogs = sorted((ROOT / "crew-agents").glob("*/catalog.json"))
    crew_templates: dict[str, Path] = {}
    if not crew_catalogs:
        errors.append("playbooks/crew-agents: no Crew catalogs found")
    for catalog_path in crew_catalogs:
        try:
            catalog = json.loads(catalog_path.read_text())
            agents = catalog["agents"]
        except (OSError, json.JSONDecodeError, KeyError, TypeError) as exc:
            fail(errors, catalog_path, f"invalid Crew catalog: {exc}")
            continue
        if catalog.get("schema_version") != 1 or not isinstance(agents, list):
            fail(errors, catalog_path, "Crew catalog needs schema_version 1 and an agents list")
            continue
        for index, agent in enumerate(agents):
            agent_id = agent.get("id") if isinstance(agent, dict) else None
            if not isinstance(agent_id, str) or not KEBAB_CASE.fullmatch(agent_id):
                fail(errors, catalog_path, f"agents[{index}] needs a canonical id")
            elif agent_id in crew_templates:
                fail(errors, catalog_path, f"duplicate Crew id {agent_id} also used by {crew_templates[agent_id].relative_to(ROOT.parent)}")
            else:
                crew_templates[agent_id] = catalog_path
    packages = sorted(
        path.parent
        for path in ROOT.rglob("playbook.json")
        if "templates" not in path.parts
    )
    if not packages:
        print("No playbook packages found", file=sys.stderr)
        return 1
    manifests: list[tuple[Path, dict[str, object]]] = []
    for package in packages:
        manifest = validate_package(package, errors)
        if manifest is not None:
            manifests.append((package / "playbook.json", manifest))

    by_id: dict[object, Path] = {}
    sibling_orders: dict[tuple[tuple[object, ...], object], Path] = {}
    for path, manifest in manifests:
        playbook_id = manifest.get("id")
        if playbook_id in by_id:
            fail(errors, path, f"duplicate id also used by {by_id[playbook_id].relative_to(ROOT.parent)}")
        else:
            by_id[playbook_id] = path

        hierarchy = manifest.get("hierarchy")
        if isinstance(hierarchy, list):
            key = (tuple(hierarchy), manifest.get("order"))
            if key in sibling_orders:
                fail(errors, path, f"duplicate order within hierarchy also used by {sibling_orders[key].relative_to(ROOT.parent)}")
            else:
                sibling_orders[key] = path

    known_ids = set(by_id)
    for path, manifest in manifests:
        validate_crew_bindings(path, manifest, set(crew_templates), errors)
        relations: list[object] = []
        if manifest.get("setup_playbook"):
            relations.append(manifest["setup_playbook"])
        for field in ("setup_playbooks", "next_playbooks"):
            values = manifest.get(field, [])
            if isinstance(values, list):
                relations.extend(values)
            else:
                fail(errors, path, f"{field} must be a list")
        for relation in relations:
            if relation not in known_ids:
                fail(errors, path, f"unknown related playbook {relation!r}")
    if errors:
        print("Playbook validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    contract_suites = sorted((ROOT / "scripts").glob("test_*_artifacts.py"))
    contract_suites.extend(package / "scripts" / "test_validate_handoff.py" for package in packages if (package / "scripts" / "test_validate_handoff.py").is_file())
    for suite in contract_suites:
        try:
            result = subprocess.run(
                [sys.executable, str(suite)], capture_output=True, text=True, timeout=30, check=False
            )
        except subprocess.TimeoutExpired:
            fail(errors, suite, "contract test timed out")
            continue
        if result.returncode != 0:
            fail(errors, suite, f"contract test failed: {(result.stderr or result.stdout)[-1200:].strip()}")
    if errors:
        print("Playbook contract validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"Validated {len(packages)} playbook packages and {len(contract_suites)} contract suites against agentworks-playbook/v1")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
