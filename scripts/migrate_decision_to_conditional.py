#!/usr/bin/env python3
"""Migrate `decision` step type to `scripted` + `conditional` pair.

Each decision step:
  { id, title, description, success_criteria, context_dependencies,
    context_output, validation_schema,
    decision_evaluation_question, if_true_next_step_id, if_false_next_step_id }

becomes two steps inserted consecutively in the plan:

  1. scripted: id=<orig>, keeps description/success_criteria/context_deps/
     context_output/validation_schema — it's the "do the work" half.

  2. conditional: id=<orig>-route, title="Route: <orig title>",
     context_dependencies=[<context_output>] (if non-empty),
     condition_question=<decision_evaluation_question>,
     if_true_next_step_id=<orig>, if_false_next_step_id=<orig>.
     Empty if_true_steps/if_false_steps arrays — the ID fields drive routing.

Upstream steps that pointed to <orig>'s ID still flow into <orig> (the scripted
half). Execution proceeds in array order → <orig>-route runs next and routes.

Run:
  python3 scripts/migrate_decision_to_conditional.py [--dry-run] [--apply]

Without --apply, writes *.migrated.json next to each plan.json for review.
With --apply, creates plan.json.pre-decision-migration.bak then overwrites.
"""

import argparse
import copy
import glob
import json
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLAN_GLOB = os.path.join(
    REPO, "workspace-docs", "Workflow", "**", "plan.json"
)


def migrate_steps(steps):
    """Return (new_steps_list, migrations) where migrations is a list of
    (orig_id, new_route_id) tuples for reporting."""
    new_steps = []
    migrations = []
    for step in steps:
        # Recurse into conditional branches first.
        for key in ("if_true_steps", "if_false_steps"):
            if isinstance(step.get(key), list):
                sub_steps, sub_migrations = migrate_steps(step[key])
                step[key] = sub_steps
                migrations.extend(sub_migrations)

        if step.get("type") != "decision":
            new_steps.append(step)
            continue

        orig_id = step["id"]
        route_id = f"{orig_id}-route"

        # Scripted half: strip decision-only fields, keep execution fields.
        scripted = {
            "type": "scripted",
            "id": orig_id,
            "title": step.get("title", ""),
            "description": step.get("description", ""),
            "success_criteria": step.get("success_criteria", ""),
            "context_dependencies": step.get("context_dependencies") or [],
            "context_output": step.get("context_output", ""),
            "has_loop": False,
            "loop_condition": "",
        }
        if "validation_schema" in step and step["validation_schema"] is not None:
            scripted["validation_schema"] = step["validation_schema"]

        # Conditional half: routing only. The evaluation question can reference
        # either the scripted half's output OR the original inputs (the decision
        # step had both in scope), so mirror both into the conditional's deps.
        context_output = step.get("context_output", "")
        orig_deps = step.get("context_dependencies") or []
        cond_deps = list(orig_deps)
        if context_output and context_output not in cond_deps:
            cond_deps.append(context_output)

        conditional = {
            "type": "conditional",
            "id": route_id,
            "title": f"Route: {step.get('title', orig_id)}",
            "description": f"Evaluate output of '{step.get('title', orig_id)}' and route accordingly.",
            "success_criteria": "",
            "context_dependencies": cond_deps,
            "context_output": "",
            "condition_question": step.get("decision_evaluation_question", ""),
            "condition_context": "",
            "if_true_steps": [],
            "if_false_steps": [],
            "if_true_next_step_id": step.get("if_true_next_step_id", ""),
            "if_false_next_step_id": step.get("if_false_next_step_id", ""),
        }

        new_steps.append(scripted)
        new_steps.append(conditional)
        migrations.append((orig_id, route_id))

    return new_steps, migrations


def process_plan(path, apply_changes):
    with open(path, "r") as f:
        plan = json.load(f)

    orig = copy.deepcopy(plan)
    steps = plan.get("steps") or []
    new_steps, migrations = migrate_steps(steps)

    if not migrations:
        return None

    plan["steps"] = new_steps

    if apply_changes:
        backup = path + ".pre-decision-migration.bak"
        with open(backup, "w") as f:
            json.dump(orig, f, indent=2)
        with open(path, "w") as f:
            json.dump(plan, f, indent=2)
        return migrations, path, backup
    else:
        preview = path.replace("plan.json", "plan.migrated.json")
        with open(preview, "w") as f:
            json.dump(plan, f, indent=2)
        return migrations, path, preview


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true", help="Rewrite plan.json in place (backup created)")
    args = ap.parse_args()

    paths = sorted(glob.glob(PLAN_GLOB, recursive=True))
    total = 0
    for p in paths:
        if "/versions/" in p:
            continue
        result = process_plan(p, args.apply)
        if result is None:
            continue
        migrations, src, out = result
        total += len(migrations)
        wf = src.split("/Workflow/")[1].split("/")[0]
        print(f"[{wf}] {len(migrations)} decision → scripted+conditional")
        for orig_id, route_id in migrations:
            print(f"    {orig_id} → {orig_id} (scripted) + {route_id} (conditional)")
        print(f"    wrote: {out}")

    if total == 0:
        print("No decision steps found.")
        return 0
    print(f"\nTotal: {total} decision step(s) migrated across {len(paths)} plans")
    if not args.apply:
        print("(preview mode — re-run with --apply to rewrite plan.json in place)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
