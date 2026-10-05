# Runner CI adoption (MOS-940)

CI keeps its current GitHub-hosted runner unless the repository variable
`MOSAIC_RUNNER_CI_ENABLED` is exactly `true`. Fork pull requests always stay
hosted. The job, matrix names, permissions and test/build commands stay the same;
checkout removes persisted Git credentials. No production credentials are added.

## Activation

Before setting the variable, retain a MOS-636 qualification receipt for the exact
release/image and bounded pool: successful and failed real jobs, long execution,
timeout and cancellation, unregister, VM/TAP/disk cleanup, isolation/egress and
rollback. Install the dedicated Runner GitHub App on this repository, provision
its controller credentials through the approved path, and configure the exact
repository/installation allowlist and signed webhook delivery. Reserve sufficient
capacity for the full matrix; broader fleet placement needs MOS-635 dispatch.
Record the qualified pool/release, operator and activation time in MOS-940.

Then set the repository variable `MOSAIC_RUNNER_CI_ENABLED=true`. Jobs from
same-repository pushes and pull requests request `[self-hosted, mosaic-runner]`.
A source merge alone does not enable Runner. This selector is an operator rollout
switch, not a live release/readiness check or automatic failover mechanism.

## Promotion evidence

Use matched source revisions and equivalent cache states to compare the existing
hosted and Mosaic executions. Retain every attempt, exact check/matrix name,
queue/setup/execution/full-feedback timestamps, results and cleanup receipts.
Treat reported initial roughly 2x faster execution as a workload-specific result
that needs its original receipts and representative internal validation. Separate
hosted-runner acquisition failures from actual execution timeouts. Adopt Runner
for daily product use and faster feedback; a dollar-savings threshold is not a
gate. Preserve independent external verification and customer capacity.

## Rollback

Unset the variable or set it to `false` to return future job scheduling to the
original hosted label. Already queued/running jobs keep their scheduling choice:
inspect and cancel/reconcile them separately. Do not automatically rerun a started
side-effecting job. Stop admission if pool qualification, cleanup, isolation or
customer-impact gates fail. Required check names must remain unchanged.
