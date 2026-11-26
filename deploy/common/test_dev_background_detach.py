#!/usr/bin/env python3
"""PLAT-746-936: stub listeners survive launcher exit in separate sessions."""
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import time

root = Path(__file__).resolve().parents[2]
with tempfile.TemporaryDirectory(prefix=".detach-test-", dir=root) as temp:
    folder = Path(temp)
    service = folder / "listener.py"
    service.write_text('''import json, os, socket, sys
s=socket.socket()
s.bind(("127.0.0.1",0))
s.listen()
with open(sys.argv[1],"w") as f:
 json.dump({"pid":os.getpid(),"sid":os.getsid(0),"port":s.getsockname()[1],"stdin":os.read(0,1).decode()},f)
while True:
 c,_=s.accept()
 with c:
  c.recv(4)
  c.sendall(b"pong")
''')
    launcher = folder / "launcher.sh"
    launcher.write_text('''#!/bin/bash
set -eu
source "$1/scripts/dev-processes.sh"
for name in workspace agent frontend; do
 dev_detached_exec "$2" "$3/listener.py" "$3/$name.json" >> "$3/$name.log" 2>&1 < /dev/null &
done
# Readiness only, not a wait for the long-lived services.
for attempt in $(seq 1 200); do
 if [ -s "$3/workspace.json" ] && [ -s "$3/agent.json" ] && [ -s "$3/frontend.json" ]; then exit 0; fi
 sleep 0.02
done
exit 1
''')
    process = subprocess.Popen(["bash", str(launcher), str(root), sys.executable, str(folder)],
                               start_new_session=True, stdin=subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    records = []
    try:
        out, err = process.communicate(timeout=15)
        assert process.returncode == 0, (out, err)
        for name in ("workspace", "agent", "frontend"):
            records.append(json.loads((folder / f"{name}.json").read_text()))
        # Simulate a terminal hangup of the original launcher's group.
        try:
            os.killpg(process.pid, signal.SIGHUP)
        except ProcessLookupError:
            pass
        for record in records:
            assert record["sid"] == record["pid"] != process.pid, "service still in launcher session"
            assert record["stdin"] == "", "service inherited terminal input"
            with socket.create_connection(("127.0.0.1", record["port"]), timeout=2) as conn:
                conn.sendall(b"ping")
                assert conn.recv(4) == b"pong", "listener died with launcher"
        print("PASS: three detached stub listeners survive launcher exit and original-group hangup")
    finally:
        # Only exact processes created by this regression; never scan or stop app services.
        for name in ("workspace", "agent", "frontend"):
            path = folder / f"{name}.json"
            if path.exists():
                try:
                    os.kill(json.loads(path.read_text())["pid"], signal.SIGTERM)
                except ProcessLookupError:
                    pass
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=5)
