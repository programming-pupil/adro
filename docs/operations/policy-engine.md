# Capability and data-egress policy

`core/policy` is the deterministic policy contract used before a protected dispatch. Decisions are based on explicit capabilities and immutable scope metadata. Tool names, prompts, and adapter self-reporting are not authority.

A frozen bundle contains a policy identity and version, tenant/workspace scope, allowed and denied capabilities, and exact HTTPS egress rules. Each egress rule binds a destination, purpose, and maximum sensitivity. Canonicalization sorts sets, normalizes destinations, rejects overlap and duplicates, and computes a canonical digest.

Every normalized input binds:

- tenant and workspace;
- actor identity;
- required capability;
- destination and purpose when data leaves the runtime;
- payload sensitivity.

The built-in evaluator returns a closed outcome and stable reason code. Its decision record stores the normalized input digest, bundle digest, policy identity/version, engine version, outcome, reason, and evaluation timestamp. It never stores the payload. `Replay` uses the historical record without re-evaluating current policy. `Audit` can recompute against the pinned bundle and reports divergence without replacing history.

`ValidateChild` rejects a delegated bundle unless every capability and egress rule is equal to or narrower than its parent. A child cannot change tenant/workspace scope, add a capability or destination, change purpose, or raise the maximum sensitivity.

`EvaluateFailClosed` converts evaluator errors, timeouts, missing results, malformed decisions, and engine-version mismatches into a deny record. Invalid caller metadata is rejected before evaluation because it cannot produce trustworthy evidence.

## Extension dispatch boundary

A signed extension manifest must declare network and data-egress permissions together. Every exact HTTPS destination/purpose must be covered by a domain, IP, or CIDR network grant and every network grant must carry a matching data-egress obligation. Registry installation and runtime startup both validate this relation.

For an extension with an egress grant, ordinary `Call` is rejected. The caller must use `CallWithEgress` with explicit tenant, workspace, actor, destination, purpose, and sensitivity. The supervisor evaluates the registry-issued grant and requires a `ports/policy.DecisionRecorder` to persist the decision before JSON-RPC or in-process dispatch. A denied decision, recorder failure, evaluator outage, or scope mismatch prevents the adapter call.

Health checks carry no data payload and remain outside the egress path. Extensions without signed egress grants cannot opt into egress at call time.

## Remaining production work

The reference boundary does not replace a production policy service or network proxy. Remaining work includes production composition of the EventStore-backed decision store, wiring every model/tool/MCP/HTTP dispatch through the same contract, tenant policy ceilings and session overrides, controlled DNS/proxy enforcement, Inspector explanations, policy bundle distribution, and cross-process compatibility/fault evidence.
