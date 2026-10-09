# Native ARM64 releases (PLAT-754)

The shared release builder and rootless activation support Linux `x86_64`
and `aarch64`. Build on a host with the target architecture: the agent links
the matching sherpa-onnx libraries with cgo. The manifest records the native
architecture and glibc version, and activation still refuses incompatible
hosts. `--arch amd64|arm64` defaults to the native architecture and refuses a
mismatch before building.

Run the **Build native release** GitHub workflow with `arch=arm64` to build
on `ubuntu-24.04-arm`. It pins all three source revisions, builds the complete
release including voice libraries, verifies the manifest, and rehearses a
product's staging step without activating services. The output artifact
contains a tarball, preserving executable bits and symlinks. It is also
available for `amd64` builds. A verification commit with
`Verify-ARM-Release: true` runs the same ARM workflow through **Verify change**.

ARM build folders use `<builder-sha8>-arm64-<timestamp>`. Publishing appends
`-arm64` to the existing three-revision release tag, so the same source
revisions can be published for both architectures without replacing each
other. Local and published retention keep builds separately per architecture.
Existing x86 names and tags stay the same.

To build on a native ARM Linux host instead, use the existing command with
`--arch arm64` and the three exact source revisions:

```sh
deploy/common/build-release.sh --arch arm64 --builds-dir /srv/_builds \
  --source mcp-agent-builder-go <repository-url> <40-hex-revision> \
  --source mcpagent <repository-url> <40-hex-revision> \
  --source multi-llm-provider-go <repository-url> <40-hex-revision>
```

Host provisioning remains deployment-specific and belongs in the private
deployments repository. It must install ARM versions of Node, ffmpeg and the
browser, and verify the coding CLIs, Docker and sandbox behavior on the actual
host. A successful build or staging rehearsal does not verify those runtime
requirements. The default x86 build host cannot produce a cgo ARM release;
use the native workflow or a separate ARM build host.
