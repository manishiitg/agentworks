---
name: animated-learning-video
description: Create child-ready animated lessons and explainer videos with HyperFrames, delivered as playable SparkQuill activities.
---

# Animated learning video

Create child-ready animated lessons and explainer videos with HyperFrames, delivered as playable SparkQuill activities.

Use this in Parent Mode when a parent asks for a video, animated explanation or visual lesson. Use the child's known grade, reading level, language, interests and school material. If the topic is missing, inspect the recent learning context and ask one focused question; otherwise proceed. A useful default is a 60–90 second landscape lesson explaining one idea through a concrete example, a visual explanation and a recap. Honour the parent's requested length and style.

## Prepare HyperFrames only when needed

All generation happens in the parent's workspace. The child receives finished local media, never a renderer or an external service. Read [references/production.md](references/production.md) for the setup commands and delivery contract. Use `execute_shell_command` for local CLI operations.

Install or refresh the official HyperFrames skills inside the production folder, read its `hyperframes/SKILL.md` entry point, and follow the selected workflow and domain references. Use its faceless-explainer workflow for narrated topic explanations and motion-graphics for a short unnarrated animation. The child-learning brief and the parent's existing choices are the input; do not make them repeat those choices. Read hyperframes-core before authoring composition HTML.

## Teach through the visuals

Keep one idea per scene. Use objects, diagrams, characters and transformations that explain the concept; avoid a sequence of text-only cards. Use large, high-contrast labels, gentle transitions and enough time to read. Keep examples and vocabulary at the child's level, verify numerical examples with Python, and match notation to school material.

For internet pictures, prefer Google Images through `agent_browser`: open the result's source page, download the actual image into the production project and inspect it before use. Retain the source and any required credit. Use local assets in the composition so rendering does not depend on remote image URLs. Generated illustrations and SVG diagrams remain available when they fit the lesson better.

Write the script before animation so the explanation, captions and visuals agree. Use narration when an available voice engine supports the requested language; report a missing voice dependency honestly and offer a captioned lesson if appropriate. Keep speech clear over music. Include a readable transcript and a pause-and-think prompt. A transcript is a learning aid, never evidence that a voiceover was generated.

## Finish the activity

Validate and visually inspect the composition, render the MP4 when authorised by the parent's request, and verify the finished media. Keep the editable project under the parent's `work/video-projects/`; copy only finished child-safe media into `activities/<yyyy-mm-dd>-<slug>/`.

Write `watch.sq.html` with a responsive `<video controls playsinline preload="metadata" src="lesson.mp4">`, a readable transcript, a recap and an optional tutor-reviewed question. No autoplay, remote player or iframe. Finalize it with `create_learning_activity`, inspect the render report, then call `open_activity`. Only the parent's Give button hands it to the child. A plan, still image or unrendered composition is not a finished video.
