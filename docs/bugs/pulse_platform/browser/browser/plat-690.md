[← browser / browser](index.md)

# PLAT-690: Browser recording lost: encoder fell behind

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | A 30 fps .webm extension recording on RTS (2 vCPU) fell behind the encoder and the whole take was lost; deploys did not check ffmpeg's video encoders |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-07 11:10–11:11 UTC: a Code chat recorded the owner's study-room session through the Chrome extension.
`record stop` failed with "Recording encoder fell more than 500 ms behind capture" and nothing was saved. The
take ran at agent-browser's default 30 fps.

## Cause

agent-browser (0.38.2 on the server) pipes captured frames to ffmpeg and abandons the take when the encoder falls
500 ms behind. RTS has 2 vCPUs. Measured on RTS, at low priority, 10 s of 1440x900 at 30 fps took 8.5 s to encode as
`.webm` (libvpx realtime) and 2.4 s as `.mp4` (libx264 ultrafast). So a `.webm` take at 30 fps has almost no headroom,
and the page's animation plus server load pushed it over.

## Fix

- Extension recordings without `--fps` record at 15 fps (`withDefaultRecordingFPS`). Test:
  `TestRecordStartDefaultsTo15FPS`.
- `browser-usage.md`: prefer `.mp4`, keep the default fps unless smooth motion is needed, and take a short test first.
- ffmpeg is a checked requirement: both deploy paths (AWS `build-and-activate.sh` and rootless `deploy.sh`) now fail
  when ffmpeg lacks libx264 or libvpx. Checked read-only on 2026-10-07: RTS, Excellence, Confida and SparkQuill pass.
  Dominion's deploy is owned by another agent and is unchanged.

## Live check left

After deploy, on RTS: a one-minute extension recording without `--fps`, and one as `.mp4`, both saved.
