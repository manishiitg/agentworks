"""Core dashboard MP4 export, run only through the authenticated workspace shell."""
import hashlib
import base64
import json
import os
from pathlib import Path
import re
import resource
import shutil
import subprocess
import sys
from datetime import datetime, timezone


def run(args, cwd, env, timeout=480):
    def file_limit():
        resource.setrlimit(resource.RLIMIT_FSIZE, (256 * 1024 * 1024, 256 * 1024 * 1024))
    result = subprocess.run(args, cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            timeout=timeout, check=False,
                            preexec_fn=file_limit if "render" in args else None)
    # Command output is diagnostic evidence, never mixed with the JSON result.
    print(result.stdout[-16000:], file=sys.stderr)
    if result.returncode:
        raise RuntimeError("The video export could not finish. Ask the Builder to check the export log and try again.")
    return result.stdout


def export():
    config = json.loads(base64.b64decode(sys.argv[1], validate=True))
    missing_tools = [name for name in ["npm", "npx", "node", "ffmpeg", "ffprobe"] if not shutil.which(name)]
    if missing_tools:
        raise RuntimeError("Video export is not available on this server yet. Missing tools: " + ", ".join(missing_tools))
    root = Path.cwd().resolve()
    source = root / config["composition_path"]
    output = root / config["output_path"]
    # The transport supplies canonical paths and a folder guard. Keep the copy
    # within that same source, including on non-sandboxed development hosts.
    source = source.resolve(strict=True)
    source.relative_to(root)
    if not (source / "index.html").is_file():
        raise ValueError("Save the animation's index.html in the composition folder first.")
    total = 0
    for entry in source.rglob("*"):
        if entry.is_symlink():
            raise ValueError("Use local copies of animation assets instead of symbolic links.")
        if entry.is_file():
            total += entry.stat().st_size
            if total > 128 * 1024 * 1024:
                raise ValueError("The animation source is too large to export (128 MB maximum).")
    snapshot_bytes = (source / "snapshot.json").read_bytes()
    snapshot = json.loads(snapshot_bytes)
    if not isinstance(snapshot, dict) or not snapshot.get("as_of") or not snapshot.get("source") or "data" not in snapshot:
        raise ValueError("snapshot.json needs as_of, source and data from the selected dashboard results.")
    datetime.fromisoformat(str(snapshot["as_of"]).replace("Z", "+00:00"))
    output.mkdir(parents=True, exist_ok=True)
    project = output / "source"
    shutil.copytree(source, project, ignore=shutil.ignore_patterns("node_modules", ".git", ".hyperframes"))
    (project / "snapshot.json").write_bytes(snapshot_bytes)
    hashes = {str(p.relative_to(project)): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in sorted(project.rglob("*")) if p.is_file()}
    # Reuse a workspace-local package cache, separate from immutable source.
    env = os.environ.copy()
    env["npm_config_cache"] = str(root / config["cache_path"])
    if not env.get("HYPERFRAMES_BROWSER_PATH") and env.get("AGENT_BROWSER_EXECUTABLE_PATH"):
        env["HYPERFRAMES_BROWSER_PATH"] = env["AGENT_BROWSER_EXECUTABLE_PATH"]
    version = config["renderer_version"]
    if not isinstance(version, str) or not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("Could not resolve a HyperFrames version.")
    cli = ["npx", "--yes", "--package", "hyperframes@" + version, "hyperframes"]
    reported_version = run(cli + ["--version"], project, env, 180)
    if not re.search(r"(?<![0-9])" + re.escape(version) + r"(?![0-9])", reported_version):
        raise RuntimeError("The server did not load the pinned animation renderer.")
    diagnostic = run(cli + ["doctor", "--json"], project, env, 180)
    doctor = json.loads(diagnostic[diagnostic.index("{"):])
    passed = {item["name"] for item in doctor.get("checks", []) if item.get("ok")}
    # Doctor's Version check means "latest on npm", not "installed correctly".
    # We deliberately use the code-reviewed pin, verified above.
    missing = {"Node.js", "FFmpeg", "FFprobe", "Chrome"} - passed
    if missing:
        raise RuntimeError("Video export is not available on this server yet. Missing tools: " + ", ".join(sorted(missing)))
    run(cli + ["lint"], project, env, 90)
    video = output / "animation.mp4"
    run(cli + ["render", "--quality", "looks", "--output", str(video)], project, env)
    probe = json.loads(run(["ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", str(video)], project, env, 30))
    streams = [s for s in probe.get("streams", []) if s.get("codec_type") == "video"]
    if not streams or video.stat().st_size == 0:
        raise ValueError("The export did not produce a playable video.")
    run(["ffmpeg", "-v", "error", "-i", str(video), "-f", "null", "-"], project, env, 90)
    result = {"success": True, "path": config["output_path"] + "/animation.mp4",
              "created_by": config.get("caller", ""),
              "source_path": config["output_path"] + "/source",
              "snapshot_path": config["output_path"] + "/source/snapshot.json",
              "as_of": snapshot["as_of"], "source": snapshot["source"],
              "renderer": "hyperframes@" + version,
              "created_at": datetime.now(timezone.utc).isoformat(),
              "duration_s": float(probe["format"]["duration"]),
              "width": streams[0]["width"], "height": streams[0]["height"],
              "bytes": video.stat().st_size, "source_sha256": hashes}
    (output / "export.json").write_text(json.dumps(result, indent=2))
    print(json.dumps(result))


if __name__ == "__main__":
    try:
        export()
    except Exception as error:
        detail = type(error).__name__ + ": " + str(error)
        print("Dashboard video export: " + detail, file=sys.stderr)
        message = str(error)
        if not isinstance(error, (ValueError, RuntimeError)):
            message = "The video export could not start. Try again, or ask the Builder to check its setup."
        print(json.dumps({"success": False, "error": message}))
        sys.exit(1)
