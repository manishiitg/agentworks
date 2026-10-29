#!/usr/bin/env python3
"""Repeatable, no-model PLAT-296 probe using a disposable testing-workflow copy."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

REPO = Path(__file__).resolve().parents[1]
SOURCE = REPO / "workspace-docs/Workflow/testing"
INPUTS = ("workflow.json", "planning/plan.json", "planning/step_config.json")


def fingerprint(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def prepare(destination):
    """Allowlist only design inputs; do not copy DBs, credentials or runtime state."""
    before = {}
    for relative in INPUTS:
        source = SOURCE / relative
        if source.is_symlink() or source.resolve() != source.absolute():
            raise RuntimeError(f"Symlinked fixture input refused: {relative}")
        before[relative] = fingerprint(source)
    destination.mkdir(parents=True)
    for relative in INPUTS[1:]:
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(SOURCE / relative, target)
        target.chmod(0o600)
    original = json.loads((SOURCE / "workflow.json").read_text())
    # This is a projection fixture, not permission to execute the source plan.
    fixture = {
        "schema_version": original.get("schema_version", 1),
        "id": "testing-projection-fixture", "label": "testing (isolated projection test)",
        "version": original.get("version", ""), "schedules": [],
        "capabilities": {"selected_servers": [], "selected_skills": [],
                         "selected_tools": [], "selected_secrets": [],
                         "selected_global_secret_names": []},
        "pulse": {"enabled": False},
        "backup": {"enabled": False}, "publish": {"enabled": False},
    }
    (destination / "workflow.json").write_text(json.dumps(fixture, indent=2) + "\n")
    (destination / "DO-NOT-EXECUTE.txt").write_text(
        "Projection test fixture only. Original plan retained as read-only evidence; "
        "it may contain external actions. No workflow/agent execution is allowed here.\n")
    return before


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expect", choices=("collision", "isolated"), default="collision",
                        help="expectation for the deliberately shared-directory negative control; private runtime cases always require isolation")
    parser.add_argument("--server-isolation", action="store_true",
                        help="verify server-selected private directories with real provider projection")
    parser.add_argument("--providers", nargs="+", choices=("codexcli", "claudecode", "cursorcli", "picli"),
                        default=["codexcli", "claudecode", "cursorcli", "picli"])
    parser.add_argument("--provider-repo", type=Path, default=REPO.parent / "multi-llm-provider-go")
    args = parser.parse_args()
    provider_repo = args.provider_repo.resolve(strict=True)
    parent = REPO / ".local/workflow-tests"
    parent.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="plat-296-", dir=parent)).resolve()
    fixture = root / "workspace-docs/Workflow/testing"
    before = prepare(fixture)
    print(f"Artifacts: {root}", flush=True)
    template = (REPO / "scripts/testdata/plat296_projection_test.go.tmpl").read_text()
    definitions = {
        "codexcli": {
            "PROMPT_WRITER": "return writeCodexProjectAgentsFile(dir, marker)",
            "SKILL_WRITER": "err = (&CodexCLIAdapter{}).ProjectSkills(dirs[role], []*llmtypes.Skill{skill})",
            "PROMPT_FILE": "AGENTS.md", "SKILLS_DIR": ".agents/skills",
        },
        "claudecode": {
            "PROMPT_WRITER": "path, err := writeClaudeCodeProjectInstructionFile(dir, marker); return func(){removeFiles([]string{path})}, err",
            "SKILL_WRITER": "err = (&ClaudeCodeInteractiveAdapter{}).ProjectSkills(dirs[role], []*llmtypes.Skill{skill})",
            "PROMPT_FILE": "CLAUDE.md", "SKILLS_DIR": ".claude/skills",
        },
    }
    definitions.update({
        "cursorcli": {
            "PROMPT_WRITER": "return prepareCursorProjectFiles(dir, marker, &llmtypes.CallOptions{}, marker)",
            "SKILL_WRITER": "err = (&CursorCLIAdapter{}).ProjectSkills(dirs[role], []*llmtypes.Skill{skill})",
            "PROMPT_FILE": ".cursor/rules/mlp-system.mdc", "SKILLS_DIR": ".cursor/skills",
        },
        "picli": {
            "PROMPT_WRITER": "return preparePiProjectFiles(dir, marker, &llmtypes.CallOptions{})",
            "SKILL_WRITER": "err = (&PiCLIAdapter{}).ProjectSkills(dirs[role], []*llmtypes.Skill{skill})",
            "PROMPT_FILE": ".pi/APPEND_SYSTEM.md", "SKILLS_DIR": ".pi/skills",
        },
    })
    # Do not source live .env files or forward provider keys. No CLI is launched.
    env = {key: os.environ[key] for key in ("PATH", "HOME", "LANG", "USER") if key in os.environ}
    (root / "tmp").mkdir()
    env.update({"TMPDIR": str(root / "tmp"), "GOWORK": "off", "GOPROXY": "off",
                "GOSUMDB": "off", "GOFLAGS": "", "AGENTWORKS_PROJECTION_TEST_ROOT": str(root),
                "AGENTWORKS_PROJECTION_TEST_FIXTURE": str(fixture),
                "AGENTWORKS_PROJECTION_EXPECT": args.expect})
    results = []
    started = time.time()
    try:
        if args.server_isolation:
            command = ["go", "test", "-mod=readonly", "./pkg/cliruntime", "./cmd/server",
                       "-run", "^Test(PLAT296WorkflowCLIDirectories|WorkflowCLIIsolationSelectionAndResume|PrepareRefusesUnsafeStorage)$",
                       "-count=1", "-timeout=120s", "-v"]
            log = root / "server-isolation.log"
            with log.open("w") as out:
                completed = subprocess.run(command, cwd=REPO / "agent_go", env=env,
                                           stdout=out, stderr=subprocess.STDOUT, timeout=300)
            output = log.read_text()
            ran = "=== RUN   TestPLAT296WorkflowCLIDirectories" in output
            passed = completed.returncode == 0 and ran and (root / "server-runtime-directories.json").exists()
            results.append({"provider": "server-runtime-selection", "passed": passed,
                            "test_executed": ran, "returncode": completed.returncode, "log": str(log)})
            if not passed:
                raise RuntimeError(f"Server isolation check failed: {log}")
            env["AGENTWORKS_PROJECTION_SERVER_DIRS"] = str(root / "server-runtime-directories.json")
            print(f"server runtime selection: verified ({log})", flush=True)
        for provider in args.providers:
            values = {"PACKAGE": provider, **definitions[provider]}
            rendered = template
            for key, value in values.items():
                rendered = rendered.replace("{{" + key + "}}", value)
            test_file = root / f"{provider}_projection_test.go"
            test_file.write_text(rendered)
            virtual = provider_repo / f"pkg/adapters/{provider}/zz_plat296_projection_probe_test.go"
            if virtual.exists():
                raise RuntimeError(f"Overlay path already exists: {virtual}")
            overlay = root / f"{provider}-overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(virtual): str(test_file)}}))
            command = ["go", "test", "-mod=readonly", f"-overlay={overlay}",
                       f"./pkg/adapters/{provider}", "-run", "^TestPLAT296TestingWorkflowProjection$",
                       "-count=1", "-timeout=60s", "-v"]
            log = root / f"{provider}.log"
            with log.open("w") as out:
                completed = subprocess.run(command, cwd=provider_repo, env=env,
                                           stdout=out, stderr=subprocess.STDOUT, timeout=300)
            output = log.read_text()
            ran = "=== RUN   TestPLAT296TestingWorkflowProjection" in output
            passed = completed.returncode == 0 and ran
            results.append({"provider": provider, "passed": passed, "test_executed": ran,
                            "returncode": completed.returncode, "log": str(log)})
            print(f"{provider}: {'expected behavior verified' if passed else 'FAILED'} ({log})", flush=True)
    finally:
        unchanged = all(fingerprint(SOURCE / path) == digest for path, digest in before.items())
        receipt = {"scope": "server runtime selection + adapter projection; not live CLI, authorization or UI E2E" if args.server_isolation else "adapter projection; not live CLI, authorization or UI E2E",
                   "expectation": args.expect, "source": str(SOURCE), "source_hashes": before,
                   "source_inputs_unchanged": unchanged, "fixture": str(fixture),
                   "model_calls": 0, "servers_started": 0, "elapsed_seconds": round(time.time()-started, 2),
                   "results": results}
        (root / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    if not unchanged or len(results) != len(args.providers) + int(args.server_isolation) or not all(r["passed"] for r in results):
        raise SystemExit(1)
    print("Source inputs unchanged. No AgentWorks services or native CLI sessions were started/stopped.")
    print(("Collision reproduced; server-selected private directories passed." if args.server_isolation else "Collision reproduced; private-directory control passed.") if args.expect == "collision"
          else "Shared-directory regression assertions passed; full session verification still required.")


if __name__ == "__main__":
    main()
