# Account capability and backoff admission

`CapabilityAdmission.tla` retrospectively captures the account-model panic
observed on 2026-11-17 in `f6e9a9a5`. It models two requests sharing one origin,
one possible origin outage, one source-change operation, and a changing role
grant. TLC explores all reachable states within these bounds. It is a safety
model, with no fairness or eventual-service-recovery claim.

The current composition checks five invariants:

| Invariant | Boundary protected |
|---|---|
| `NoPanic` | A wait observer cannot dereference a missing gate. |
| `ObserverOwnsGate` | The observer uses the gate that admits its request. |
| `CallsRequireHTTP` | A terminal call record requires an admitted upstream attempt; cancelling a wait alone is not a call. |
| `CapabilityRequired` | A request needs the role grant sampled at capability admission. |
| `SourcePolicyRespected` | Source changes wait for admitted calls and live streams; frozen source policy refuses new calls. |

`TypeOK` is checked alongside these invariants. Eight independent `Never*`
witnesses demonstrate reachable waits, cancelled waits, completed calls, live
streams, capability refusals, source-policy refusals, concurrent calls and a
source change after completion. A witness must fail, rather than quietly pass
because its scenario is unreachable.

## Connection to the Go code

| Model action | Implementation boundary | Real regression |
|---|---|---|
| `CheckCapability` | `Pool.guardCapability` preserves `trackedModel.policyUse`; `recordingModel.admit` acquires the source hold. | `TestRoleUsesPublicResponsesAndRefusesAfterGrantRemoval` removes one role while the conversation source remains healthy. |
| `WaitNotice` | `recordingModel.admitBackoff` passes the recorder to `backoffGate.acquire`. | `TestModelBackoffCanceledBeforeHTTPRecordsOnlyTheWait/account` crosses `ModelBuilder` and the real HTTP adapter. |
| `SendHTTP`, `Complete`, `Cancel` | `generateOnce` / `streamOnce` begin accounting after admission and settle the actual request. | `TestAccountModelRecoversThroughOneAdmissionAndRecorder` covers Chat Completions and Responses, Generate and Stream. |
| `OpenStream` | `streamOnce` retains admission until the stream completes or is cancelled. | `TestModelStreamHoldsSourceUntilUpstreamCompletes/account` refuses a source change while the real HTTP stream remains open. |
| `AcquireSourceChange` | `Pool.TryHoldModelPolicy` uses the writer side of the shared policy lock. | Both source-hold regressions above check refusal while active and availability after completion. |

The historical `NestedCapabilityWrapper` switch restores the outer recorder's
missing gate. It must violate both `NoPanic` and `ObserverOwnsGate` in separate
cases. Adding `GuardNilObserverOnly` suppresses that callback but leaves early
outer accounting: cancellation must still violate `CallsRequireHTTP`. The real
Go cancellation regression rejects both variants, for the original nil-pointer
stack and for the extra call record respectively.

The other seeded switches remove the role check, drop the inherited source
policy callback, or release it at stream creation. Each must violate its named
invariant and is mapped to the real HTTP regression in the table. These are
seeded guard removals, not additional reported production incidents.

## Checks

```bash
make model-check
python3 -m unittest tools.test_model_check
python3 -m unittest tools.test_model_check.WithTLC.test_capability_composition_finds_known_crash_and_guard_removals
go test -race -timeout 5m ./internal/provider
```

`make model-check` includes all 15 capability-admission cases. The Python
registration tests reject missing crash/partial-fix cases or unreachable witness
coverage. The TLC test runs the whole family when the pinned JAR and Java are
installed. The checker retains complete per-case configurations, model copies,
TLC output and counterexample traces outside the repository.

The gate clock, exponential delay, recovery-probe health/generation, SDK parsing,
Go memory safety and lock implementation are abstracted, not established by this
model. A `sent` request is an upstream attempt after admission, not a guarantee
that a network packet reached a remote server. Role grants are sampled at the
existing admission boundary; later revocation does not retroactively cancel an
already admitted request. HTTP regressions and race tests remain required.
The other provider model, `RouteRecovery.tla`, checks consecutive route-refusal
caps and their separation from ordinary model retries.
