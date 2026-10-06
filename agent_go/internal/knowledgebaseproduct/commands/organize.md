Organize Brain: tidy the folder named below (and the way to organize it, if I gave one) (or the whole Brain if none is named) so people and agents can find things. Act directly; this command is my explicit ask to move, merge and rewrite notes in folders I can edit.

{{context}}

## How to organize

A folder tree has one main hierarchy; the other ways of looking at the same knowledge are kept as views (index notes that link, never copies).

- Main hierarchy, in this order of precedence: the structure the folder's (or the Brain root's) `readme.md` describes; a mode I named above; otherwise **by products**. Modes:
  - **by products** (default): `Products/<product>/` holds what each product is, does and how it runs.
  - **by teams**: `Teams/<team>/` holds each team's knowledge; shared subjects stay in the subject folders below.
  - **by entities**: `Entities/` holds one page per person, customer, system and vendor (`Entities/People/`, `Entities/Customers/`, `Entities/Systems/`, `Entities/Vendors/`).
- Views, always kept current whatever the mode: `Timeline/` (what happened when) and the `Entities/` index (one page per person, customer, system and vendor, linking every note about it). Teams and products not chosen as the main hierarchy get an index note listing their notes.
- Never organize by workflow or company name: the Brain is the company; knowledge used by one workflow only stays in that workflow.

Default layout (by products):

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
Teams/<team>/             a team's own working knowledge (or index, when not the main hierarchy)
Entities/                 People/, Customers/, Systems/, Vendors/: one page per entity
Decisions/                one note per decision: what, why, who, when
Timeline/<year>/<year>-<month>.md   one line per event, newest first
Skills/                   company skills, one folder per skill (SKILL.md)
Sources/                  imported docs, meeting notes, raw material
```

## Rules

- Work only through brain_browse, brain_read and brain_update, in folders where I am Editor or Owner. Never change access.
- If Git backup is configured, run brain_backup status first and say whether there are unbacked changes; do not push unless I asked.
- One topic per note, kebab-case filenames, a type (fact, note, source, skill). A note covering two subjects is split.
- Duplicates: find notes on the same subject even with different names or wording; keep the best-placed, most complete one, merge every fact from the others into it (newest wins on a conflict; when unsure keep both and mark the conflict), then delete the others. A merge is the only reason to delete.
- Move misplaced notes to the folder their subject belongs in. Create folders as needed; split a folder holding more than about 15-20 notes.
- Every folder you touch gets a `readme.md`: what belongs there, links to its notes and subfolders.
- Decisions live once, in `Decisions/`, updated in place with who and when; other notes link to them.
- Timeline: for every dated decision, release, incident or notable change you find, make sure `Timeline/<year>/<year>-<month>.md` has a line: `YYYY-MM-DD · type · one-line summary → path`. Add missing lines; never rewrite past ones.
- Facts in topic notes carry an "as of" date when you can tell it; flag notes that look stale instead of guessing.
- Workflows and Crews read Brain by folder path. When you move a note, leave nothing broken: update links between notes, and list every moved path in your report so the workflows that read them can be updated.

## Report

A short summary: the mode used; what you moved, merged (kept path and removed paths), split, created and deleted; Timeline lines and entity pages added; notes flagged stale; and anything you left because it needs a person to decide.
