# ADRO design notes

ADRO is an independent, plugin-first delivery control plane. The kernel owns
requirements, Bugs, projects, Agent roles, workflow state, context manifests,
evidence and audit. A local execution plugin discovers the installed coding
client and runs it in a bounded worktree; Git, CI, deployment, notification,
memory and data access integrations are separate SPI modules.

The primary product invariant is continuity: a failed test creates a repair
attempt that returns to the original development session and worktree. The
engine accepts the repair only when the session, worktree and evidence records
match. Employees govern objectives, permissions and exceptions; Agents perform
the actual engineering, testing and analysis work.
