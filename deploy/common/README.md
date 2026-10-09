# Linux release retention

All deployments that write `releases/<id>` share `prune-releases.py`:

| Deployment | Application directory |
| --- | --- |
| Video Studio rootless | `/var/lib/video-studio/video-studio` |
| Confida rootless | `/srv/confida` |
| Legacy EC2 installer | `/opt/video-studio` |
| EC2 rootless migration | `/var/lib/video-studio/video-studio` |

After successful activation, agent and workspace health endpoints must return
JSON with `status: healthy`. Cleanup keeps the current release and any older
release referenced by a running process. Uploads/builds marked `.deploying`
are protected; deployment exit traps remove these markers after failures too,
so a later successful deployment can remove incomplete copies. If an SSH
connection is lost and marker removal fails, clear the marker once the upload
is confirmed stopped.

No rollback archive is kept after successful deployment. Application data,
logs, source checkouts, unknown directories, and symlink targets are not removed.
Container images built from docker-compose.yml are not release directories.
This helper does not prune container images, registries, or Docker volumes.

Preview or apply cleanup on a server:

```bash
python3 prune-releases.py /srv/confida
python3 prune-releases.py /srv/confida --apply \
  --health-url http://127.0.0.1:22000/api/health \
  --health-url http://127.0.0.1:22001/health
```

Run the cleanup and deployment-wiring regression checks locally:

```bash
python3 -m unittest discover -s deploy/common -p 'test_*.py'
```
