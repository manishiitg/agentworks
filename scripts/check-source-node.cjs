#!/usr/bin/env node
// #2908: dashboard preparation imports .ts directly; Node 20 cannot run it.
function supportedSourceNode(version) {
  const [major, minor] = version.split('.').map(Number)
  return major >= 24 || (major === 22 && minor >= 18)
}
module.exports = { supportedSourceNode }
if (require.main === module && !supportedSourceNode(process.versions.node)) {
  console.error(`AgentWorks source builds need Node.js 22.18+ (22.x) or 24+. Found ${process.versions.node}. Upgrade Node.js, then rerun the launcher.`)
  process.exitCode = 1
}
