"""Prove Muse's real Stop continuation with isolated per-run user hooks.

Usage: python3 .../prove_muse_hooks.py /absolute/path/to/muse-binary
No plugin approval, credentials, persistent user settings or network required.
The shared CLI response is a fixture: this proves Muse formatting, not delivery.
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


PLUGIN = Path(__file__).resolve().parents[1]


def main():
    binary = Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="agentworks-muse-native-") as directory:
        scratch = Path(directory).resolve()
        for path in ("config/muse", "bin", "workspace"):
            (scratch / path).mkdir(parents=True)
        # A project-controlled script must never shadow the installed package.
        project_hooks = scratch / "workspace/hooks"
        project_hooks.mkdir()
        shadow_marker = scratch / "project-hook-executed"
        for script in ("stop.py", "prompt_submit.py", "session_end.py", "runtime.py"):
            (project_hooks / script).write_text(
                "from pathlib import Path\n"
                "Path(" + repr(str(shadow_marker)) + ").write_text('unsafe project hook')\n"
                "raise SystemExit('PROJECT_HOOK_SHADOW_866')\n"
            )
        calls = scratch / "calls.jsonl"
        executions = scratch / "python.jsonl"
        python = scratch / "bin/python3"
        python.write_text(
            "#!" + sys.executable + "\n"
            "import json,os,pathlib,sys\n"
            "with pathlib.Path(" + repr(str(executions)) + ").open('a') as f:\n"
            " f.write(json.dumps({'argv':sys.argv[1:],'cwd':os.getcwd()})+'\\n')\n"
            "os.execv(" + repr(sys.executable) + ",[" + repr(sys.executable) + ",*sys.argv[1:]])\n"
        )
        python.chmod(0o700)
        cli = scratch / "bin/agentworks"
        cli.write_text(
            "#!" + sys.executable + "\n"
            "import sys,json,pathlib\n"
            "assert sys.argv[1:] == ['plugin','hook','muse']\n"
            "p=json.load(sys.stdin)\n"
            "with pathlib.Path(" + repr(str(calls)) + ").open('a') as f: f.write(json.dumps(p)+'\\n')\n"
            "if p['hook_event_name']=='Stop':\n"
            " print(json.dumps({'decision':'block','reason':'MUSE_STOP_FIXTURE_866'}))\n"
            "elif p['hook_event_name']=='UserPromptSubmit':\n"
            " print(json.dumps({'hookSpecificOutput':{'hookEventName':'UserPromptSubmit',"
            "'additionalContext':'MUSE_PROMPT_FIXTURE_866'}}))\n"
        )
        cli.chmod(0o700)
        hooks = {
            event: [{"hooks": [{"type": "command", "command":
                    "python3 " + str(PLUGIN / "hooks" / script), "timeout": 10}]}]
            for event, script in (("Stop", "stop.py"), ("UserPromptSubmit", "prompt_submit.py"),
                                  ("SessionEnd", "session_end.py"))
        }
        (scratch / "config/muse/settings.json").write_text(json.dumps({"schema_version": 1, "hooks": hooks}))
        env = dict(os.environ, XDG_CONFIG_HOME=str(scratch / "config"),
                   XDG_DATA_HOME=str(scratch / "data"), XDG_CACHE_HOME=str(scratch / "cache"),
                   PATH=str(scratch / "bin") + os.pathsep + os.environ["PATH"],
                   PYTHONDONTWRITEBYTECODE="1")
        # Native capability paths are files, not shell-expanded variables.
        for index, variable in enumerate(("${MUSE_PLUGIN_ROOT}", "${PLUGIN_ROOT}")):
            invalid = scratch / ("invalid-plugin-" + str(index))
            shutil.copytree(PLUGIN, invalid)
            manifest = invalid / ".muse-plugin/plugin.json"
            content = json.loads(manifest.read_text())
            for hook in content["capabilities"]["hooks"]:
                hook["command"][1] = variable + "/" + hook["command"][1]
            manifest.write_text(json.dumps(content))
            validation = subprocess.run([str(binary), "plugins", "validate", str(invalid), "--json"],
                                        cwd=scratch / "workspace", env=env, capture_output=True,
                                        text=True, timeout=15)
            assert validation.returncode != 0, "Muse unexpectedly expanded a plugin-root variable"
            assert variable in validation.stdout + validation.stderr
        command = [str(binary), "exec", "--provider", "echo", "--disable-reminders",
                   "--no-foreign-personal-context", "--disable-write", "--disable-shell",
                   "--disable-web-tools", "--no-session-log", "--max-model-steps", "4", "--json",
                   "MUSE_FIRST_TURN_866"]
        installed = subprocess.run([str(binary), "plugins", "install", str(PLUGIN),
                                    "--scope", "user", "--json"], cwd=scratch / "workspace",
                                   env=env, capture_output=True, text=True, timeout=15, check=True)
        installation = json.loads(installed.stdout)
        cache_path = Path(installation["installed"]["cache_path"]).resolve()
        assert cache_path.is_relative_to(scratch)
        assert not installation["diagnostics"]
        inspected = subprocess.run([str(binary), "plugins", "inspect", "agentworks-muse", "--json"],
                                   cwd=scratch / "workspace", env=env, capture_output=True,
                                   text=True, timeout=15, check=True)
        capabilities = json.loads(inspected.stdout)["plugin"]["capabilities"]
        sources = {}
        for hook in capabilities["hooks"]:
            source = Path(hook["source_path"])
            assert source.is_absolute() and source.is_relative_to(cache_path)
            assert source.name in ("stop.py", "prompt_submit.py", "session_end.py")
            sources[hook["id"]] = source.resolve()
        # hook test is an explicit fixture runner; it does not approve runtime hooks.
        session = "4c67acbd-e8c1-4f8a-9c26-9d4feeb403d7"
        for capability, event in (("stop", "Stop"), ("prompt-submit", "UserPromptSubmit"),
                                  ("session-end", "SessionEnd")):
            executions.unlink(missing_ok=True)
            fixture = scratch / "fixture.json"
            fixture.write_text(json.dumps({"event": event, "stdin": {
                "session_id": session, "hook_event_name": event, "stop_hook_active": False,
                "prompt": "MUSE_FIRST_TURN_866"}}))
            checked = subprocess.run([str(binary), "plugins", "hook", "test",
                                      "agentworks-muse:" + capability, "--fixture", str(fixture), "--json"],
                                     cwd=scratch / "workspace", env=env, capture_output=True,
                                     text=True, timeout=15, check=True)
            decision = json.loads(checked.stdout)["decision"]
            if event == "Stop":
                assert decision["should_block"] and decision["block_reason"] == "MUSE_STOP_FIXTURE_866"
            elif event == "UserPromptSubmit":
                assert decision["additional_contexts"] == ["MUSE_PROMPT_FIXTURE_866"]
            else:
                assert not decision["should_block"]
                assert not decision["additional_contexts"]
            execution = [json.loads(line) for line in executions.read_text().splitlines()]
            assert len(execution) == 1
            assert len(execution[0]["argv"]) == 1
            script = Path(execution[0]["argv"][0])
            cwd = Path(execution[0]["cwd"])
            assert script.is_absolute(), execution
            assert script.resolve() == sources[capability], execution
            assert script.resolve().is_relative_to(cache_path), execution
            assert cwd == scratch / "workspace", execution
            print("Native " + event + ": absolute cached " + str(script.relative_to(cache_path))
                  + "; cwd=hostile workspace")
            assert not shadow_marker.exists(), "Muse executed a project hook instead of the cached plugin"
        calls.unlink()
        result = subprocess.run(command, cwd=scratch / "workspace", env=env,
                                capture_output=True, text=True, timeout=25, check=True)
        events = [json.loads(line) for line in result.stdout.splitlines() if line.startswith("{")]
        deltas = [event for event in events if event.get("payload_type") == "run.output.delta"]
        terminal = [event for event in events if event.get("payload_type") == "run.terminal.completed"]
        assert len(deltas) == 2 and len(terminal) == 1, "Stop did not continue exactly once"
        inputs = [json.loads(line) for line in calls.read_text().splitlines()]
        assert [item["hook_event_name"] for item in inputs] == ["UserPromptSubmit", "Stop", "SessionEnd"]
        assert not inputs[1]["stop_hook_active"]
        assert len({item["session_id"] for item in inputs}) == 1
        assert len({event["stream"]["id"] for event in deltas}) == 1
        assert not shadow_marker.exists()
    print("PASSED: all three native hooks execute cached absolute paths; root variables rejected; "
          "source user hooks continue Stop once with exact-session lifecycle")


if __name__ == "__main__":
    main()
