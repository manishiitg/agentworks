[← platform / frontend-chat](index.md)

# PLAT-494 — The launcher's `npm install` blocked every start when node_modules was half-installed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | fixed on main: the launcher installs only when the lockfile changed and carries on with a usable node_modules if the install fails. |

| Coordination | Value |
|---|---|
| State | fixed on main; takes effect on the next launch |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Source

`run_server_with_logging.sh` ran `npm install` on every start (since 2026-06-01) and exited if it failed. On 2026-10-05 a
half-finished install left a temp folder and a stray `node_modules/node_modules` in the main checkout, `npm install` failed with
ENOENT/ENOTDIR, and the frontend could not start although the packages already there would have worked.

## Done

- `ensure_frontend_deps` runs `npm install` only when `package.json` or `package-lock.json` changed since the last successful install
  (a stamp in `node_modules`). Otherwise it skips.
- If the install fails but `node_modules/.bin/vite` exists, it warns (with the `rm -rf node_modules && npm ci` repair command) and starts anyway.
  It still stops when there is nothing usable.
- Checked with a fake `npm`: first install, unchanged skip, changed + failing install with vite present (warn), failing with nothing usable (stop).

## Left

- The damaged `node_modules` in the main checkout still needs `rm -rf node_modules && npm ci` once (the helper cannot repair it).
- The desktop (Electron) dependency install is unchanged: it only runs when the Electron binary is missing.

## Register notes

[PLAT-494](plat-494.md), fixed on main: the launcher installs only when the lockfile changed and carries on with a
usable node_modules if the install fails.
