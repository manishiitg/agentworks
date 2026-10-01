---
name: google-ai
description: "Generate optional still images and off-camera narration through Google's Gemini API for Video Studio. Do not use Google video models: all Video Studio footage uses MiniMax H3 through fal.ai."
---

# Google Gemini stills and narration

Google's own Gemini image generation (publicly nicknamed “Nano Banana”) and
Gemini TTS are reached directly through Google's Gemini API, not through
fal.ai. In Video Studio, use this skill only for optional still-image work or
off-camera narration. Never generate footage with Veo, Gemini Omni, or any
other Google video model: all paid video uses MiniMax H3 through `fal-ai`.

This skill owns the client call after the still-image or narration direction is
decided. Read `video-look-sound` for narration choices and
`video-cinematography` for an optional reference-image brief.

## Never invent a model ID

Google image and TTS product names are not API model identifiers. Do not guess
the exact model string from memory. Before the first still-image or narration
call in a new production:

1. Resolve the current Google model ID and required request shape from Google's
   own Gemini API reference -- not from this skill, which is deliberately
   silent on exact IDs and changes on Google's own schedule.
2. Record the resolved model ID and exact request in the production record so a
   revision reuses the same asset setup rather than whatever is current later.

A run whose model ID cannot be confirmed is a blocker to report, not a guess
to make.

## Authentication

The user stores the key as a workflow secret named `GEMINI_API_KEY` (via
`set_workflow_secret`) -- the same env var name this platform's own LLM
provider configuration already uses for Gemini, kept consistent rather than
introducing a second name for the same kind of credential. The
secret-injection mechanism prefixes every secret name with `SECRET_` in the
shell environment, so the variable actually present is
`$SECRET_GEMINI_API_KEY`, not `$GEMINI_API_KEY`. Bridge the two explicitly:

```bash
export GEMINI_API_KEY="$SECRET_GEMINI_API_KEY"
node generate.mjs
```

or pass it directly to the client instead of relying on ambient env:

```js
import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({
  apiKey: process.env.SECRET_GEMINI_API_KEY ?? process.env.GEMINI_API_KEY,
});
```

Accept either name: `SECRET_GEMINI_API_KEY` is what the injection mechanism
provides, but a key set directly in the environment as `GEMINI_API_KEY` is
equally valid and should not be treated as missing. If neither is set, stop
and report the blocker -- do not proceed without generation credentials, and
never print or log the key value itself.

## Environment

Google's officially supported client for this is the Node.js SDK
(`@google/genai`), matching this product's existing preference for a managed
Node toolchain over ad hoc scripts in another language. No project-local
`package.json`/`node_modules` is assumed to exist yet:

```bash
npm init -y --silent
npm install --silent @google/genai
```

Write generation scripts as plain `.mjs` files and run them with `node`.

## The generation call

Confirm the exact method name and request shape against the model reference
you resolved -- image and video generation are separate calls on this client
and their request/response shapes differ by model:

```js
import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({ apiKey: process.env.SECRET_GEMINI_API_KEY });

const response = await ai.models.generateContent({
  model: "<resolved-model-id>",
  contents: "<prompt, and any reference image per the model's input spec>",
});
// Confirm the exact response shape (inline image bytes vs. a hosted URL)
// against the resolved still-image model's own reference; it moves
// independently of this skill.
```

## Generating narration (text-to-speech)

Gemini generates optional off-camera speech through the same client. It never
replaces the Fal key required for H3 footage. Two things about the output differ
from every other call in this skill and both will produce
a broken file if missed:

- **The audio comes back as raw PCM, not a playable file.** It is base64
  16-bit PCM at 24 kHz, mono. Writing those bytes to `.wav` unchanged
  produces a file nothing will play -- wrap it in a WAV header first (or
  convert with ffmpeg, `-f s16le -ar 24000 -ac 1`). Confirm the returned
  sample rate rather than assuming, since it is stated per model.
- **Quality drifts on long passages.** Google's own guidance is to split
  anything beyond a few minutes into chunks. This matches what
  `video-editing` already requires for a different reason -- narration is
  generated per beat or per chapter so one bad beat can be regenerated
  without re-timing everything after it -- so generate per segment and
  measure each with `ffprobe`, never as one long pass.

```js
const response = await ai.models.generateContent({
  model: "<resolved-tts-model-id>",
  contents: [{ parts: [{ text: "<the exact narration line>" }] }],
  config: {
    responseModalities: ["AUDIO"],
    speechConfig: {
      voiceConfig: { prebuiltVoiceConfig: { voiceName: "<voice>" } },
    },
  },
});
// base64 PCM -- wrap as WAV before writing, per above.
const pcm = response.candidates[0].content.parts[0].inlineData.data;
```

Confirm the model id, the voice list, the exact response path, and whether
your SDK version prefers this or a newer speech endpoint against Google's
own current speech-generation reference before relying on it -- this
surface is in preview and moves faster than the rest of the API. Keep one
voice for the whole production unless the script genuinely calls for more
than one speaker; multi-speaker output is supported but capped at two.

## Music is not covered here

Google's API surface in this skill covers video, image, and speech -- not a
music bed. A production that needs one and has no fal.ai key should use an
uploaded or licensed track (see `video-editing` for the levels to mix it
at), or run without music rather than substituting something whose licence
is unclear. Say which of those is happening rather than quietly shipping a
silent mix the brief did not ask for.

## Sending a local file as input

Unlike `fal-ai`, which uploads a file and passes back a URL, Gemini takes
image input inline as base64 bytes alongside the prompt. This is how a
character reference image produced per `video-cinematography` gets
conditioned on:

```js
import { readFile } from "node:fs/promises";

const bytes = await readFile("work/productions/<slug>/characters/<name>.png");

const response = await ai.models.generateContent({
  model: "<resolved-model-id>",
  contents: [
    { inlineData: { mimeType: "image/png", data: bytes.toString("base64") } },
    { text: "<prompt>" },
  ],
});
```

Confirm the exact `contents` shape against the resolved model's own
reference before relying on it -- part ordering, field names, and whether a
model accepts more than one reference image all vary, and the larger-file
path (an upload/file API rather than inline bytes) differs again. Record
which reference image conditioned which asset in the production record so a
revision reuses the same one.

Treat a call that errors or times out as a real failure to report with the
error payload -- do not silently retry with different input hoping one
succeeds, and do not fabricate a result if generation fails.

## Output handling

- Depending on the model, output arrives as inline bytes or a hosted URL --
  check which before writing download/decode logic. Get every retained asset
  into its production's `work/` directory before the session ends.
- Use stable, descriptive filenames per shot/asset, and record them in
  `production.json` alongside the model ID and request that produced them.
- Verify what was actually generated before treating a call as done: inspect
  still-image dimensions or listen to the narration segment. A successful API
  response is not proof the asset is usable.

## Cost awareness

Every call here is a paid API request. Stay within the user's approved cost
limit, avoid paid multi-variant generation unless they explicitly asked for
alternatives, and cache every successful generation so a revision reuses the
downloaded file instead of regenerating it. Re-running a generation call merely
to re-inspect its own output is a cost bug, not a verification step -- inspect
the file you already downloaded.

## Where this fits

This skill produces raw generated assets only. It does not assemble a video
or judge whether one is ready to present:

- Use `longform-cinematic-video` to own the overall brief and shot plan.
- Use `video-look-sound` to decide whether off-camera TTS is appropriate.
- Use `video-editing` to assemble narration and music into a final cut.
- Use `video-quality` before presenting any version as complete.
