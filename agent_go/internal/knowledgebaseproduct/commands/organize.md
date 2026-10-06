Organize Brain: tidy the folder named below (or the whole Brain if none is named) so people and agents can find things. Act directly; this command is my explicit ask to move, merge and rewrite notes in folders I can edit.

{{context}}

## Target layout

If the folder (or Brain root) has a `readme.md` that describes its own structure, follow that. If I named a layout above, follow that. Otherwise use this default, by subject (never by workflow, team or company name; the Brain is the company):

```
readme.md                 map of the Brain: what lives where
Company/                  who we are, customers, glossary, policies
Products/<product>/       overview, features, user flows, roadmap, releases
Engineering/
  Architecture/<system>/  services, repos, data flow
  Infrastructure/         cloud, environments, deploys, logging
  Runbooks/               how to operate or fix X
  Standards/              coding, review, testing conventions
Operations/
  Monitoring/  Costs/  Security/  Incidents/
Teams/<team>/             a team's own working knowledge
Decisions/                one note per decision: what, why, who, when
Timeline/<year>/<year>-<month>.md   one line per event, newest first
Skills/                   company skills, one folder per skill (SKILL.md)
Sources/                  imported docs, meeting notes, raw material
```

## Rules

- Work only through brain_browse, brain_read and brain_update, in folders where I am Editor or Owner. Never change access.
- If Git backup is configured, run brain_backup status first and say whether there are unbacked changes; do not push unless I asked.
- One topic per note, kebab-case filenames, a type (fact, note, source, skill). A note covering two subjects is split; two notes on one subject are merged into the better one, keeping every fact, and the other is deleted (a merge is the only reason to delete).
- Move misplaced notes to the folder their subject belongs in. Create folders as needed; split a folder holding more than about 15-20 notes.
- Every folder you touch gets a `readme.md`: what belongs there, links to its notes and subfolders.
- Decisions live once, in `Decisions/`, updated in place with who and when; other notes link to them.
- Timeline: for every dated decision, release, incident or notable change you find, make sure `Timeline/<year>/<year>-<month>.md` has a line: `YYYY-MM-DD · type · one-line summary → path`. Add missing lines; never rewrite past ones.
- Facts in topic notes carry an "as of" date when you can tell it; flag notes that look stale instead of guessing.
- Workflows and Crews read Brain by folder path. When you move a note, leave nothing broken: update links between notes, and list every moved path in your report so the workflows that read them can be updated.

## Report

A short summary: what you moved, merged, split, created and deleted (with paths), Timeline lines added, notes flagged stale, and anything you left because it needs a person to decide.
