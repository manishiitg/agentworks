# SparkQuill slash commands

SparkQuill loads its own commands from the parent and child profiles in
`agent_go/internal/sparkquillproduct/product.yaml`. Parent Mode offers 14 commands;
Child Mode offers six commands scoped to the current activity. The shared
composer shows the active profile's commands.

| Parent commands | Purpose |
| --- | --- |
| `/create-learning-game`, `/create-interactive-quiz` | Complete, play-checked activities using the learning-game skill. |
| `/create-animated-video`, `/create-reading-book` | Finished HyperFrames videos or illustrated books with page navigation. |
| `/create-study-material` | Explanations, worked examples, practice and a recap. |
| `/create-study-material-ib`, `/create-study-material-cbse`, `/create-study-material-icse` | Study activities matched to the child's board and grade. |
| `/create-a-practice-test` | A checked test with agreed help/timing and a parent-only answer key. |
| `/surprise-her` | An immersive game on a new topic chosen for the child's interests. |
| `/update-progress-report`, `/run-check-in`, `/check-school-websites` | Evidence-based progress and school updates. |
| `/back-up-workspace` | A verified backup using the family's backup configuration. |

Child commands are `/score-by-answer`, `/recap`, `/walk-me-through-my-mistakes`,
`/check-strictly`, `/keep-quizzing-me-harder-each-time` and
`/give-me-a-real-example`. They use actual activity evidence and respect the
parent's test, timing and help rules. The adaptive quiz adjusts to the child;
it has no fixed question count.

Command names come from the manifest, independently of the menu label. Manifest aliases and hidden menu entries follow the shared platform contract.

Add a topic or preferences after a command, for example
`/create-reading-book fractions, Grade 4, 12 pages`. Selecting a command from the
picker also retains text around the slash. SparkQuill uses `renderCommandPrompt`
to fill `{{context}}`, omits empty context labels for a bare command, and retains typed context. These are
starting instructions; the agent chooses the creative structure and scope.
