# Local HyperFrames production

Use a dedicated folder such as `work/video-projects/<yyyy-mm-dd>-<slug>/` under the family workspace. Run setup there, not in a child's activity or a global skill directory. HyperFrames needs Node.js 22+, FFmpeg, FFprobe and Chrome for local rendering; inspect the doctor output for the actual missing prerequisite. Optional hosted services are not required for a local captioned animation.

```sh
npx --yes hyperframes@latest skills update
npx --yes hyperframes@latest doctor --json
```

The first command installs the current official core skill set. Locate its SKILL.md files under this project's `.agents/skills/` or the coding provider's skill folder; read `hyperframes/SKILL.md` first. If setup fails, preserve the project, report the observed problem and do not claim a video is ready. Do not use remembered vendor instructions in place of missing skills.

The router installs the selected workflow on demand. Common commands, run from the production project, are:

```sh
npx --yes hyperframes@latest skills update faceless-explainer
npx --yes hyperframes@latest init composition --non-interactive --example blank --resolution landscape
```

Use the generated project's pinned CLI scripts thereafter so validation and render use the same version. Read the installed CLI references for the actual flags. Before rendering, `check` must pass and scene snapshots must show readable, unclipped content. A request for a completed video authorises the local render; stop at a preview if the parent specifically asked to review it first.

The composition and its runtime scripts are editable production sources, not a SparkQuill `.sq.html` worksheet. Do not pass the HyperFrames composition through `create_learning_activity`; the worksheet renderer may remove assets required by HyperFrames. Render first, then wrap the MP4 in an ordinary SparkQuill activity page.

Copy `lesson.mp4`, any poster and caption assets beside `watch.sq.html`. Use a quoted relative `src="lesson.mp4"` directly on `<video>`; SparkQuill rewrites relative src attributes for its sandboxed viewer. Keep the transcript in the worksheet so reading and learning still work without audio. Use inline JavaScript only for worksheet controls and the SQ bridge, not to drive the rendered video.

Inspect the final MP4 with FFprobe for a real video stream, expected dimensions and duration, and an audio stream if narration was promised. Visually inspect frames from the rendered output, including each scene and the ending. Check the player in the actual viewer where possible: play, pause, seek, replay, captions and volume when present. Keep the parent's private source notes and answer keys outside the activity folder.
