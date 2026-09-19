# Sandbox Broker

Design source: `ADRO-origin` and public operating-system isolation primitives.

`ports/sandbox` defines the execution boundary for untrusted tools and extensions. A caller submits a `SandboxRequest`, asks for a minimum cumulative enforcement level, and receives an immutable `SandboxHandle`. `Prepare` rejects the request when the selected backend cannot enforce any requested capability or resource limit. There is no automatic full-access fallback.

## Enforcement contract

The ordered enforcement levels are:

1. `none`
2. `process`
3. `filesystem`
4. `network`
5. `container`
6. `microvm`
7. `remote`

A higher level satisfies the lower cumulative levels only when the backend reports a valid capability set. Capability metadata is validated before it can be used. A test capability override may reduce detected host capabilities; it cannot advertise a different backend or upgrade unavailable enforcement.

Every request binds the handle to tenant, session, effect, backend, enforcement level, canonical request digest, and expiry. `Execute`, `Cancel`, and `Dispose` compare the complete handle. A copied ID with altered scope or digest is rejected.

The runner contract includes:

- separate file `read`, `write`, `create`, `delete`, and `execute` grants;
- exactly one domain, IP, or CIDR selector per network grant, with validated ports, protocol, purpose, and expiry;
- wall timeout and output byte limits;
- bounded, nonblocking stdout/stderr events with an explicit dropped-event count;
- cancellation and disposal;
- whole-process-group termination on supported Unix hosts;
- stable errors for timeout, cancellation, output overflow, invalid handles, path escape, denied capabilities, and unavailable enforcement.

CPU, memory, disk, or secret injection limits are rejected when the backend cannot enforce them. Output requests above the backend maximum are also rejected instead of silently clamped.

## Local developer backends

`adapters/sandbox/local` is a reference and developer backend. It must never be presented as secure tenant isolation.

On macOS, when `/usr/bin/sandbox-exec` is available, the backend uses a deny-default Seatbelt profile. It grants the resolved executable and explicit file operations, denies outbound network access by default, and enforces wall/output limits. Arbitrary domain, IP, and CIDR grants are rejected because Seatbelt alone cannot faithfully bind DNS resolution and the eventual connection target. Such grants require a controlled proxy or a production backend.

On other current hosts, the backend reports only `process` enforcement. The child command still receives wall/output limits and cancellation, but host filesystem and network access remain available. Linux Landlock/bubblewrap and Windows restricted-token/ACL implementations are not present yet, so the local adapter does not claim those controls.

The local adapter performs lexical workspace containment and rejects a symlink in every existing path ancestor. It resolves and freezes the executable before issuing a handle. Hard-link alias detection, mount-boundary controls, and TOCTOU-resistant descriptor-relative path opening remain required for production filesystem conformance.

## Lifecycle and output behavior

A prepared handle has one execution. Concurrent repeated `Execute` calls receive the same stream and cannot launch duplicate processes. Cancelling a prepared handle prevents later execution. Closing a stream cancels its execution context.

The stdout/stderr event channel has fixed capacity. Readers of the child pipes never block on a slow or absent event consumer; events can be dropped and the terminal result records the count. Captured terminal output remains bounded. Exceeding the byte limit terminates the process tree and returns `sandbox.ErrOutputLimit`.

Prepared handle expiry also bounds the execution deadline. Timeout, caller cancellation, output overflow, process exit, and startup failure produce distinct terminal errors and results.

## Evidence

The contract and negative tests are in:

- `ports/sandbox/sandbox_test.go`
- `adapters/sandbox/local/local_test.go`
- `adapters/sandbox/local/process_unix_test.go`
- `adapters/sandbox/local/profile_darwin_test.go`

The macOS suite executes real Seatbelt probes for an allowed file write, a denied ungranted write, and default outbound-network denial. Cross-platform tests cover capability rejection, path escape, ancestor and leaf symlinks, forged and expired handles, timeout, cancellation, output termination, slow consumers, frozen environment state, nonzero exit, and descendant-process cleanup. Windows source is cross-compiled, but Windows isolation conformance is not claimed.

## Production gaps

This capability is not stable. The repository still needs at least one production-conformant rootless OCI or remote-worker backend; controlled-proxy network grants with DNS rebinding protection; Linux and Windows OS isolation; hard-link and mount-boundary protection; CPU, memory, and disk enforcement; brokered secret injection; cross-tenant tests; Runtime Inspector/API wiring; and production fault evidence.
