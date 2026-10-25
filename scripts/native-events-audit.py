#!/usr/bin/env python3
"""On-demand, metadata-only native event audit over SSH or AWS SSM.

Targets and account/server details belong in a private JSON file. This command
does not import transcripts, restart processes, call models or create a service.
"""
import argparse
import base64
import json
import shlex
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path


PROBE = r'''
import collections,glob,json,os,re,shutil,subprocess,sys
from pathlib import Path
c=json.loads(sys.argv[1]); result={"name":c["name"],"transport_reachable":True,"audit_schema":"1","providers":{},"log_window":"last 8 MiB"}
for provider,binary in {"claude-code":"claude","codex-cli":"codex","cursor-cli":"cursor-agent","pi-cli":"pi","muse-cli":"muse","agy-cli":"agy"}.items():
    entry={"found_on_probe_path":bool(shutil.which(binary)),"account":"NOT VERIFIED","live_scenario":"NOT RUN"}
    if entry["found_on_probe_path"]:
        try:
            p=subprocess.run([binary,"--version"],capture_output=True,text=True,timeout=5)
            versions=re.findall(r"\b\d+\.\d+(?:\.\d+)?(?:[-+][A-Za-z0-9.]+)?",p.stdout[:2048])
            entry["version"]=versions[0] if versions else "UNKNOWN"
        except (OSError,subprocess.TimeoutExpired):entry["version"]="UNAVAILABLE"
    result["providers"][provider]=entry
states=collections.Counter(); helpers=collections.Counter(); claims=collections.Counter()
for file in glob.glob(os.path.join(c["state_root"],"native-records","*.json")):
    try:
        state=json.loads(Path(file).read_text()); provider=state.get("provider","unknown"); states[provider]+=1
        for record in state.get("helpers",{}).values(): helpers[provider+":"+record.get("state","unknown")]+=1
    except (OSError,ValueError):states["unreadable"]+=1
for file in glob.glob(os.path.join(c["state_root"],"native-reply-claims","*.json")):
    try:
        claim=json.loads(Path(file).read_text());claims[claim.get("owner","unknown")+(":committed" if claim.get("committed") else ":unfinished")]+=1
    except (OSError,ValueError):claims["unreadable"]+=1
result.update(owned_runtime_states=dict(states),helper_states=dict(helpers),publication_claims=dict(claims))
accepted={}; saved=set(); committed=set(); followups=set(); gaps=collections.Counter(); unknown=collections.Counter(); invalid=identity=0
try:
    with open(c["log_path"],"rb") as f:
        f.seek(0,2); size=f.tell(); f.seek(max(0,size-(8<<20))); lines=f.read().decode("utf-8","replace").splitlines()
    for line in lines:
        if "[NATIVE_RECORD]" not in line:continue
        fields=dict(re.findall(r"\b([a-z_]+)=([^\s]+)",line))
        if " accepted " in line and fields.get("event"):
            accepted[fields["event"]]={k:fields.get(k,"") for k in ("provider","native","turn","item","kind","state","followup")}
        if " reply_saved " in line:saved.add((fields.get("native"),fields.get("turn")))
        if " foreground_settled " in line and fields.get("committed")=="true":committed.add((fields.get("native"),fields.get("turn")))
        if " gap " in line:
            reason=fields.get("reason","").strip('"');gaps[reason if re.fullmatch("[a-z_]+",reason) else "reader_error"]+=1
        if " coverage " in line:
            invalid+=int(fields.get("invalid","0"));identity+=int(fields.get("identity_gaps","0"))
            match=re.search(r"unknown=(\{.*?\})",line)
            if match:
                try:
                    for k,v in json.loads(match.group(1)).items():
                        label=k if re.fullmatch(r"[A-Za-z0-9_.:-]{1,80}",k) else "unrecognized_schema_label"
                        unknown[label]+=v
                except ValueError:unknown["invalid_coverage_record"]+=1
    kinds=collections.Counter()
    for record in accepted.values():
        kinds[record["provider"]+":"+record["kind"]+":"+record["state"]]+=1
        if record["followup"]:followups.add((record["native"],record["followup"]))
    missing=followups-saved-committed
    result.update(observed_events=dict(kinds),gap_reasons=dict(gaps),unknown_kinds=dict(unknown),invalid_records=invalid,identity_gaps=identity,
                  followups_without_saved_reply=[{"native_session_id":n,"native_turn_id":t,"state":"AWAITING OR MISSING; inspect server evidence"} for n,t in sorted(missing)],
                  saved_observer_turns=len(saved),committed_foreground_turns=len(committed))
except OSError:result["log_status"]="UNAVAILABLE"
release=c.get("release_path")
if release:
    try:result["release"]=os.path.basename(os.path.realpath(release))
    except OSError:result["release"]="UNKNOWN"
result["interpretation"]="Installed is not signed in. Replay is not live-provider proof. No recent records is not a pass. Native turn end is not business success."
print(json.dumps(result))
'''


def run(argv, **kwargs):
    return subprocess.run(argv, check=True, capture_output=True, text=True, timeout=120, **kwargs).stdout


def audit(target):
    config = {key: target[key] for key in ("name", "state_root", "log_path")}
    if target.get("release_path"):
        config["release_path"] = target["release_path"]
    code = "import base64;exec(base64.b64decode(" + repr(base64.b64encode(PROBE.encode()).decode()) + "))"
    command = "python3 -c " + shlex.quote(code) + " " + shlex.quote(json.dumps(config))
    try:
        if target["transport"] == "ssh":
            output = run([*target["ssh"], command])
        elif target["transport"] == "ssm":
            aws = ["aws"]
            if target.get("profile"):
                aws += ["--profile", target["profile"]]
            aws += ["--region", target["region"], "ssm"]
            sent = json.loads(run([*aws, "send-command", "--instance-ids", target["instance_id"], "--document-name", "AWS-RunShellScript", "--parameters", json.dumps({"commands": [command]}), "--output", "json"]))
            command_id = sent["Command"]["CommandId"]
            for attempt in range(60):
                time.sleep(2)
                try:
                    answer = json.loads(run([*aws, "get-command-invocation", "--command-id", command_id, "--instance-id", target["instance_id"], "--output", "json"]))
                except subprocess.CalledProcessError:
                    continue
                if answer["Status"] == "Success":
                    output = answer["StandardOutputContent"]
                    break
                if answer["Status"] not in ("Pending", "InProgress", "Delayed"):
                    raise RuntimeError("remote read failed: " + answer["Status"])
            else:
                raise RuntimeError("remote read timed out")
        else:
            raise ValueError("transport must be ssh or ssm")
        return json.loads(output)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        # No raw remote stdout/stderr: those may contain unrelated private data.
        return {"name": target.get("name", "unknown"), "transport_reachable": False, "status": "UNVERIFIED", "reason": type(error).__name__}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--targets", type=Path, required=True, help="Private JSON array of SSH/SSM targets")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    targets = json.loads(args.targets.read_text())
    with ThreadPoolExecutor(max_workers=3) as pool:
        results = list(pool.map(audit, targets))
    report = {"generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "evidence": "read-only server metadata; no model scenarios executed", "servers": results}
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print("Audit saved:", args.output)
    print("Reachable:", sum(bool(x.get("transport_reachable")) for x in results), "/", len(results))


if __name__ == "__main__":
    main()
