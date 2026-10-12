#!/usr/bin/env python3
"""Check package/delegation; shared Go regressions verify kernel/lease authority."""
import json
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
    assert json.loads((package / manifest["mcpServers"]).read_text())["mcpServers"] == {}
    assert manifest["skills"] == ["./skills/agent-messages", "./skills/agentworks-messaging"]
    for skill in manifest["skills"]:
        assert (package / skill / "SKILL.md").is_file()
    source = package.parent / "shared/skills/agent-messages/SKILL.md"
    assert (package / "skills/agent-messages/SKILL.md").read_bytes() == source.read_bytes()
    hooks = json.loads((package / manifest["hooks"]).read_text())["hooks"]
    assert hooks["stop"][0]["loop_limit"] == 5
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
