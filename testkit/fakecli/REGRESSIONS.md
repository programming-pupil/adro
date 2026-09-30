The frozen baseline's process and protocol assets are retained here for V2-04.
Both families run in real child processes in their fixture tests. Fixture tests
validate the emitted input; they do not certify the runtime's terminal policy.

| Input | Fixture | Required production regression (V2-04) |
|---|---|---|
| A terminal event followed by a live child | `streamjson`, mode `linger` | `supervisor.TestReapAfterCleanExit`; also add a surviving descendant |
| Result marker only, completion only, both, neither | `streamjson` terminal modes | `driver.TestFinalizeExhaustive`; combine with exit status, shutdown grace, stop reason and resource/protocol failures |
| Matching, missing or differing session proof | `streamjson` proof modes; `framed` session mismatch and rejection modes | `session.TestProveResumeMismatch` and declared rejection checks |
| Correlated ready/session/tool/usage/result frames | `framed` | Framed family contract suite |
| A result followed by a nonzero exit | `framed`, mode `exit-after-result` | Terminal evidence cannot override an unclean exit |

The legacy result marker is deliberately retained as an input for the negative
case that assistant text cannot satisfy an execution evidence gate. It is not a
v2 result envelope and is never interpreted by these fixture packages.

Atomic-write source: `v1-final:internal/provider/local.go` (temporary snapshot
write), to be used by `supervisor.SaveAtomic` and receipt `Outbox.Put` in V2-04.
Preserve the algorithm, not the old provider's authoritative state file:

1. Encode and validate the complete bounded record before changing the file.
2. Create a unique temporary file in the destination directory.
3. Enforce mode 0600; write every byte; reject short writes and all errors.
4. Sync the file and close it, propagating either error.
5. Rename the temporary file over the destination on the same filesystem.
6. Sync the destination's parent directory with `scan.SyncParent`; a rename
   alone does not establish durable completion.
7. Remove the temporary file on every pre-rename failure and close all handles.
   A failure after rename must be reported even though new contents may exist.

V2-04 must exercise file and directory sync failures, pre-rename failure,
replacement, permissions, concurrent readers, process death at each durability
boundary, and recovery. Merely writing this migration record does not satisfy
those production tests. The old source remains present until that asset is
ported; V2-01 item 005 remains incomplete meanwhile.
