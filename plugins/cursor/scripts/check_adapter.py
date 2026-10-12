#!/usr/bin/env python3
"""Check package/delegation; shared Go regressions verify kernel/lease authority."""
import json
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile


SCRIPT = Path(__file__).with_name("cursor_hook.py")


def main():
    package = SCRIPT.parent.parent
    manifest = json.loads((package / ".cursor-plugin/plugin.json").read_text())
    assert manifest["name"] == "agentworks"
    for key in ("hooks", "mcpServers"):
        assert (package / manifest[key]).is_file()
    assert json.loads((package / manifest["mcpServers"]).read_text())["mcpServers"] == {
        "agentworks-messages": {"command": "agentworks", "args": ["plugin", "mcp", "cursor"]}
    }
    assert manifest["skills"] == ["./skills/agent-messages", "./skills/agentworks-messaging"]
    for skill in manifest["skills"]:
        assert (package / skill / "SKILL.md").is_file()
    source = package.parent / "shared/skills/agent-messages/SKILL.md"
    assert (package / "skills/agent-messages/SKILL.md").read_bytes() == source.read_bytes()
    assert json.loads((package / manifest["hooks"]).read_text())["hooks"] == {}, "Project hooks are the single registration"
    hooks = json.loads((package / "hooks/hooks.json").read_text())["hooks"]
    assert hooks["stop"][0]["loop_limit"] == 5
    spec = importlib.util.spec_from_file_location("cursor_project_setup", SCRIPT.with_name("setup_project.py"))
    setup = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(setup)
    with tempfile.TemporaryDirectory(prefix="cursor-project-") as folder:
        project = Path(folder) / "project with spaces"
        (project / ".cursor").mkdir(parents=True)
        target = project / ".cursor/hooks.json"
        prior = {"version": 1, "custom": "retained", "hooks": {"stop": [{"command": "existing-check", "loop_limit": 2}]}}
        target.write_text(json.dumps(prior))
        _, changed = setup.configure(project, package)
        assert changed
        installed = json.loads(target.read_text())
        assert installed["custom"] == "retained" and installed["hooks"]["stop"][0] == prior["hooks"]["stop"][0]
        first = target.read_bytes()
        assert setup.configure(project, package)[1] is False and target.read_bytes() == first
        # An older path must not turn an update into duplicate delivery hooks.
        installed["hooks"]["stop"].append({"command": "python3 /old/cursor_hook.py"})
        target.write_text(json.dumps(installed))
        before = target.read_bytes()
        try:
            setup.configure(project, package)
            raise AssertionError("Duplicate delivery hook accepted")
        except ValueError:
            assert target.read_bytes() == before
    with tempfile.TemporaryDirectory(prefix="cursor-wrapper-") as folder:
        root = Path(folder)
        cli = root / "agentworks"
        cli.write_text('''#!/usr/bin/env python3
import json,os,sys
with open(os.environ['REQUESTS'],'a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')
if sys.argv[1:4]==['plugin','hook','cursor']:
 event=json.load(sys.stdin)
 if event.get('hook_event_name')=='beforeSubmitPrompt' and event.get('prompt','').startswith('/agentworks '): print(json.dumps({'continue':False,'user_message':'Shared control handled'}))
else:
 print(json.dumps({'attached':True,'attachment':{'provider':'cursor','native_session_id':'session','attachment_id':os.environ.get('RETURN_ATTACHMENT','lease')},'result':{'delivery':'stored'}}))
''')
        cli.chmod(0o700)
        log = root / "requests"
        env = dict(os.environ, AGENTWORKS_CLI=str(cli), REQUESTS=str(log), PYTHONDONTWRITEBYTECODE="1")
        def run(event=None, args=(), extra=None):
            return subprocess.run(["python3", str(SCRIPT), *args], input=json.dumps(event) if event else "",
                                  text=True, capture_output=True, env=dict(env, **(extra or {})), timeout=20)
        ordinary = {"hook_event_name":"beforeSubmitPrompt","conversation_id":"session","prompt":"Keep this prompt"}
        assert run(ordinary).stdout == ""
        assert json.loads(run(dict(ordinary,prompt="/agentworks attach receiver")).stdout)["continue"] is False
        args = ["reply","--session","session","--attachment-id","lease","--message-id","message",
                "--message","Literal $(text)","--submission-id","stable"]
        assert json.loads(run(args=args).stdout)["ok"] is True
        request = json.loads(log.read_text().splitlines()[-1])
        assert request[:5] == ["bridge","message","reply","--provider","cursor"]
        assert request[request.index("--attachment-id")+1] == "lease"
        assert request[request.index("--message")+1] == "Literal $(text)"
        assert json.loads(run(args=args,extra={"RETURN_ATTACHMENT":"other"}).stdout)["ok"] is False
        assert run(args=["ack","--session","session","--message-id","message","--delivery","admitted"]).returncode == 1
    print("Cursor package/shared delegation checks passed")


if __name__ == "__main__":
    main()
