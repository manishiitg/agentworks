// The messages the Source Control view sends to the project's agent. Anything
// that needs judgement (a commit message, a conflict, why a line changed)
// goes to the agent; the view only does the mechanical git steps itself.

const where = (repoRoot: string) => (repoRoot ? `the \`${repoRoot}\` repo` : 'this repo')

export const gitAgentPrompts = {
  commit: (repoRoot: string, hasStaged: boolean) =>
    `Review the ${hasStaged ? 'staged' : 'current'} changes in ${where(repoRoot)}, write a clear commit message and commit them. Tell me the message you used.`,
  pull: (repoRoot: string) => `Pull the latest changes for ${where(repoRoot)} and tell me what changed.`,
  push: (repoRoot: string) => `Push ${where(repoRoot)}'s commits to its remote.`,
  resolveConflict: (repoRoot: string, file?: string) =>
    file
      ? `Resolve the merge conflict in \`${file}\` in ${where(repoRoot)}: read both sides, keep the right combination, remove the conflict markers and stage the file. Explain what you chose and why.`
      : `Resolve every merge conflict in ${where(repoRoot)}: for each conflicted file read both sides, keep the right combination, remove the conflict markers and stage it. Explain what you chose and why.`,
  explainChanges: (repoRoot: string, file: string) =>
    `Explain the uncommitted changes to \`${file}\` in ${where(repoRoot)}: what changed, why it might have changed, and anything risky. Do not modify any files.`,
  reviewChanges: (repoRoot: string) =>
    `Review the uncommitted changes in ${where(repoRoot)} as a code reviewer: point out bugs, risks and missing tests. Do not modify any files.`,
  explainCommit: (repoRoot: string, hash: string, subject: string, file?: string) =>
    `Explain commit ${hash.slice(0, 7)} ("${subject}") in ${where(repoRoot)}${file ? `, focusing on \`${file}\`` : ''}: what it changed and why. Do not modify any files.`,
  explainBlame: (repoRoot: string, file: string, from: number, to: number, hash: string, summary: string) =>
    `In \`${file}\` in ${where(repoRoot)}, lines ${from}-${to} were last changed by commit ${hash.slice(0, 7)} ("${summary}"). Explain what those lines do and why they changed. Do not modify any files.`,
}
