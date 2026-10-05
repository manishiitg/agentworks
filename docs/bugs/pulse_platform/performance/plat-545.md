# PLAT-545: old release copies were never pruned and filled the RTS disk

**State:** fixed on main (scripts), not yet run on RTS. P1 while the disk is 99% full.

**Found:** 2026-10-05, RTS deploy exited 1 with `gateway: initialize project SQL tables: database or disk is full`. The root disk was 99% full (95 of 96 GB): 27 release copies, 34 GB, under `/var/lib/video-studio/video-studio/releases`, plus 12 GB of Go build cache and 5.7 GB of npm cache. The failed deploy left the previous release serving.

**Cause:** the shared pruner (`deploy/common/prune-releases.py`) aborted with `release cleanup skipped: Permission denied: '/proc/<pid>/root'` since the slot accounts were introduced (it resolves paths found in process data, and another account's `/proc/<pid>/root` cannot be resolved). The deploy only prints that as a warning, so nothing was pruned after any deploy.

**Fix:**
- The pruner skips an unresolvable path instead of aborting, and always keeps the newest releases (`--keep-newest`, default 2: the live one and the one before it) on top of the active one and anything a readable process still uses, because other accounts' processes cannot be fully inspected.
- New `--only-if-free-below-gb N`: a deploy first makes room, only when space is short (15 GB), before it delivers or builds anything. Wired into all three deployers: RTS (`deploy.sh`), the rootless-Linux products (Confida, Excellence, SparkQuill; `deploy/rootless-linux/build-and-activate.sh`) and Dominion (`deploy-dominion.sh`). A failure here only warns.
- One test pins the abort and the newest-kept rule.

**Left:** run the next RTS deploy (it prunes first; about 28 GB come back); check the other servers' disk and releases (Excellence, Confida, SparkQuill, Dominion); decide on the Go build cache (12 GB) and npm cache (5.7 GB) on RTS.
