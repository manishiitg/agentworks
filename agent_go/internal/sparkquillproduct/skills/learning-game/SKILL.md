---
name: learning-game
description: Create or improve complete playable learning games and interactive quizzes for SparkQuill, matching the child's interests and learning goals.
---

# Learning games

Use in Parent Mode for games, playful quizzes, discovery activities, or a request to make an existing activity more engaging. Use the known child, topic and interests; infer a suitable direction from the request and available evidence. Honour requests for a straightforward quiz or formal test.

Aim for a finished experience the child wants to play. Choose the genre, visual style, technology, depth and scope yourself. A substantial adventure, simulation, puzzle world or arcade game is welcome when it fits. There is no default cap on duration, levels or scenes, and no required genre or feature checklist.

## Make the learning playable

Give the child something meaningful to do: explore, build, measure, experiment, negotiate, investigate or solve. Make understanding the topic change the game's outcome. For example, measurement might control construction in a world the child can explore. Invent the design for this child; examples are inspiration, not templates.

Choose a coherent setting and make actions visibly affect it. Use responsive controls, expressive characters, animation, illustration and sound when they support the experience. For a quiz, answers can influence a mission, a simulation or a branching story; decorative badges alone do not make the activity immersive. Keep the actual thinking appropriate to the learning goal, even when the presentation is elaborate.

Let challenges develop as the child learns, with useful feedback, recovery from mistakes and a satisfying outcome. Provide hints or adaptation where they help, following the parent's requested help level. Local play can respond immediately; use the tutor when conversation adds value. Give the child clear controls and an easy way to pause or restart. Fit the actual viewer and support the intended input devices; start audio after a gesture and offer a mute control when using it.

## Build and deliver

Read [references/activity-runtime.md](references/activity-runtime.md) when implementing the game. It describes the actual renderer, asset and progress contracts so an ambitious design survives delivery. Use the admitted native and platform tools to build and inspect the finished experience. Obtain or create useful visual assets rather than defaulting to the same generic card layout.

Finalize with `create_learning_activity`, inspect its report, and play the finished HTML in the browser. Exercise the main interaction, mistakes and recovery, progression, ending and restart; check the viewer layout and any assets. Resolve broken interactions before handing over. A title screen, mock-up, planned levels or buttons awaiting future implementation are unfinished. Report a verification limitation honestly.

Call `open_activity` for the parent's preview and Give button. Describe what the child can actually play and what it teaches. Keep formal assessment solutions and parent-only notes in `keys/`, outside the child's activity.
