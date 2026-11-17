# Private service profiling

On Linux, both services watch `pprof/REQUEST` beside their log file. The agent
uses its existing `LOG_FILE`; the workspace resolves its existing stdout log
through `/proc/self/fd/1`. No listener, authentication change or configuration
change is needed. A process without a regular log file does not start the watcher.

The service creates `pprof` with mode 0700 and requires service ownership and
private permissions for the directory and marker. As that service account:

```sh
(umask 077; touch <logs-dir>/pprof/REQUEST)
```

Each process observes a new marker once, within two seconds, and records a
30-second CPU profile plus allocation snapshots before and after it. The marker
stays so both services see it; touch it again for another capture. An existing
marker at startup is ignored. Outputs are exclusive-created mode 0600, named
with service, PID and UTC start time. Symlink markers and nonprivate directories
or markers are refused. Profiles contain sampled stacks and counts, not heap
contents. Keep them private and do not serve this directory over HTTP.

`[CPU_PROFILE]` ready/started/done/failed lines identify the service and profile.
Done includes elapsed seconds, process CPU percent (one core = 100%), GC/minute,
allocated bytes and allocation count for the window. CPU sampling itself adds
some overhead. Allocation snapshots use Go's normal sampling and GC visibility;
no forced collection is added. For interval attribution, subtract the before
snapshot, rather than interpreting the cumulative after snapshot as the window:

```sh
go tool pprof -top <service>-cpu.pprof
go tool pprof -top -alloc_space -base <service>-allocs-before.pprof <service>-allocs-after.pprof
go tool pprof -top -alloc_objects -base <service>-allocs-before.pprof <service>-allocs-after.pprof
```

A capture already running in the process makes the new attempt log a failure;
it does not interrupt the existing profiler. Old profiles are left for the
operator to collect or remove.
