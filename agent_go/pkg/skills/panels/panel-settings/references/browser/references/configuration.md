## Browser connection

Browser mode and CDP settings live in the project's **Browser** view. In project
chat, `browser` lists the person's saved Chrome and Edge browsers,
returns their setup link, and selects or disconnects one for this Crew. It never
disconnects the person's other projects.

A project can use several browsers at the same time: one main browser and others
added with `add` (removed with `remove`). Each is its own Chrome or Chrome
profile with its own sign-ins, for example one profile for each work account.
`agent_browser` status lists them under `browsers`; pass `browser=<name>` on a
command to use another one, and keep using that name for the tabs it opened.
Without a name the main browser is used. `select` on another browser makes it
the main one, and the old main one stays in use as another browser.

Use `agent-browser` for an actual browser task after reading its skill;
connecting is separate from browsing.
