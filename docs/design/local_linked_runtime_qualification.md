# Local linked-runtime qualification — 2026-09-30

## Scope and result

On the user's macOS machine, the linked-directory contract passed live CLI
checks for Claude, Codex, Cursor and Pi. This is partial local qualification,
not approval to deploy all providers or certification of native confinement.
Existing chats and project data were not used or modified.

The running local agent service answered its health check. Its binary includes
project and step-output links; it predates the explicit project search hint.
Four existing project links resolved, with private runtime roots at mode 0700.
These observations were read-only. The app was not restarted.

## Live evidence

Tests ran in owned worktrees with disposable fixture data, production mcpagent
launch/resume/output-link code, the real MCP bridge, and installed CLIs. The
fixture's project link is constructed by the test; application admission and
mode guards are checked separately. No requests were sent to existing app chats.

| CLI | Installed version | Evidence | Result |
| --- | --- | --- | --- |
| Claude | 2.1.285 | Native Glob with explicit `project` and `output` paths found unknown filenames; native Read returned random fixture contents. Native Write was denied in don't-ask mode; the admitted bridge wrote through `output/`. | Pass for discovery, reads, authoritative output, native resume and cleanup. Native write permission is a separate limit. |
| Codex | 0.159.2 | Native `rg --files project` and `rg --files output` discovered unknown filenames, shell read both links, and native apply_patch wrote the real artifact. | Pass for discovery, reads, writes, native resume and cleanup. |
| Cursor | 2026.09.28-64d2043 | Native Glob and Read followed explicit link paths. Native Write was blocked by the existing orchestrator policy; the admitted bridge wrote the artifact. | Pass for discovery, reads, bridge writes, native resume and cleanup. |
| Pi | 0.99.1 | Its admitted shell bridge discovered/read both links and wrote the real artifact. | Pass with the existing Google credential supplied to the test process. Pi's native tools remain disabled by existing policy. |
| Muse | 1.4.1-R4503.1 | Simple direct CLI control prompt succeeded. Integrated linked session completed the first turn with both random file contents in the real output via the bridge; resume exceeded the shared 150-second fixture budget. | Partial artifact pass; do not claim live resume or cleanup qualification. |
| Agy | 1.2.14 | Private CLI session stopped at the Antigravity sign-in screen before executing the prompt. | Unqualified; sign-in required. |

Pi initially timed out with `No API key found for google` in its private session.
The ordinary direct CLI control prompt succeeded with ambient native auth. The
managed adapter isolates its auth/config, so the successful retry supplied the
already stored credential using Pi's own auth command, in process environment
only. No credential value was printed or committed. This does not prove the
app's provider-account plumbing for every user.

Passing fixture cases read random contents absent from the prompt, wrote them
to the authoritative output directory, closed the CLI, resumed a fresh Agent
from its saved native handle, and recalled an unwritten random word. Final
session cleanup removed the private runtime while preserving the real artifact.
The test records actual tool calls; a model's claim of success is insufficient.

## Mode and output guards

Existing targeted tests passed locally for Crew admission, distinct Run/Builder
prompts/skills/session identities, all six provider runtime identities, native
resume, Run write-grant narrowing, workflow isolation/rollback, scheduled Run
runtime separation, and linked-project persistence/obstruction handling.
Step-output tests passed for dedicated session guards, iteration/group targets,
unauthorized or escaping targets, matching output configuration, all provider
launch policies, resume and artifact preservation.

macOS does not run Linux Landlock. These local tests cannot prove kernel-level
read-only access through the link. The previously recorded Linux linked-project
and linked-output tests are separate evidence; authenticated Linux checks in
both modes are still required by the deployment decision.

## Search contract and remaining work

Search from the private cwd can skip the symlink's contents. Native tools must
receive `project` or `output` explicitly as the search root. The project guidance
is already on main. This qualification adds the equivalent `output` guidance to
mcpagent's step instructions; it does not enable additional native tools.

Muse's incomplete resume, Agy authentication, and native write permissions
need separate follow-up. Do not increase permissions to make this fixture pass.
The fixture's native tool mode is `full_unconfined` on a disposable local
workspace; it is not a test of application Run-mode authorization.

## Reproduce

In an owned mcpagent worktree, with an owned provider dependency configured:

```sh
RUN_LOCAL_LINKED_CLI=1 LOCAL_LINKED_PROVIDER=Claude \
  go test ./agent -run '^TestLocalLinkedCLIArtifactsAndResume$' -count=1 -v -timeout=4m
```

Repeat for `Codex`, `Cursor`, `Pi`, `Muse`, or `Agy`. Omitting the selector runs
all installed cases; allow a larger overall test timeout for that run. It consumes real provider requests and requires each
managed session's auth. For Pi, supply its configured provider key through the
normal provider environment; do not write credentials into fixture files or logs.
