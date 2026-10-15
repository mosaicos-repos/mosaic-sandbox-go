# Mosaic Sandbox Go SDK

The Go SDK uses only the standard library and supports Go 1.22 or newer.

## Installation

Install the tagged module through the Go proxy:

```bash
go get github.com/mosaicos-repos/mosaic-sandbox-go@v0.15.0
```

Set `MOSAIC_API_TOKEN` before making requests:

```bash
export MOSAIC_API_TOKEN='your-token'
```

## Run once

```go
package main

import (
	"context"
	"fmt"
	"log"

	mosaic "github.com/mosaicos-repos/mosaic-sandbox-go"
)

func main() {
	client, err := mosaic.New()
	if err != nil {
		log.Fatal(err)
	}
	result, err := client.RunOnce(context.Background(), mosaic.Argv("echo", "hello"), mosaic.RunOnceOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(result.Stdout)
}
```

## Functions

A function is a name you keep for a one-shot run: a specification — template,
command, secrets, network policy, resources, timeout — that lives in your code.
Defining one makes no request and creates nothing to deploy or delete. Each
invocation creates one isolated microVM, runs the command, and destroys it, so
an idle function costs nothing.

```go
thumbnail := client.Function(mosaic.Argv("python", "-m", "thumbnail"), mosaic.FunctionOptions{
	Template:     "python-3.11",
	Secrets:      []string{"OBJECT_STORE_TOKEN"},
	NetworkAllow: []string{"objects.mosaicos.com:443"},
	TimeoutMs:    60000,
})

result, err := thumbnail.InvokeWith(ctx, mosaic.InvocationOverrides{
	Env: map[string]string{"OBJECT_KEY": "images/input.jpg"},
})
if err != nil { log.Fatal(err) }
fmt.Print(result.Stdout, result.SandboxDestroyed)
```

What the sandbox is stays fixed for every invocation: `InvocationOverrides`
carries only `Cwd`, `Env`, `Stdin`, `TimeoutMs` and `IdempotencyKey`, so one
call cannot widen another's secrets or egress. Its pointer fields let an
invocation clear the specification's `Cwd` or `Stdin` rather than only replace
it, and `FunctionOptions` has no `KeepSandbox` or `IdempotencyKey` to set —
use `RunOnce` for a single run that keeps its sandbox. Retrying with the same
`IdempotencyKey` replays the first invocation instead of running a second one.

Work that must outlive one synchronous call belongs in a process or job, and
work that needs a URL belongs behind a preview.

## Long-running attempts

Synchronous exec is capped at 900,000 ms (15 minutes). A durable process is
owned by the sandbox rather than by the request that starts it, so persist the
sandbox and process IDs and reconnect after a client restart. Its lifetime is
bounded by its own timeout, when supplied, and by the sandbox TTL.

```go
ttlSeconds := 86400
sandbox, err := client.CreateSandbox(ctx, mosaic.CreateOptions{
	Template: "base", TTLSeconds: &ttlSeconds,
})
if err != nil { log.Fatal(err) }
defer sandbox.Destroy(ctx)

started, err := sandbox.Processes().Start(ctx,
	mosaic.Argv("python", "-m", "train"), mosaic.StartOptions{})
if err != nil { log.Fatal(err) }
sandboxID, processID := sandbox.ID, started.ID

sandbox, err = client.ConnectSandbox(ctx, sandboxID)
if err != nil { log.Fatal(err) }
processes, err := sandbox.Processes().List(ctx)
if err != nil { log.Fatal(err) }
var handle *mosaic.Process
for _, process := range processes {
	if process.ID == processID { handle = process; break }
}
if handle == nil { log.Fatal("process not found") }
result, err := handle.Wait(ctx)
if err != nil || !result.Success { log.Fatal("attempt failed") }
```

A running process prevents hibernation; after it finishes, pause/resume works
normally. Call `handle.Kill(ctx)` to cancel it. The full Python, TypeScript,
Go, CLI, and raw-HTTP recovery patterns are in the public documentation.

## Create, execute, and destroy

```go
client, _ := mosaic.New()
ctx := context.Background()
sandbox, err := client.CreateSandbox(ctx, mosaic.CreateOptions{Template: "base"})
if err != nil { log.Fatal(err) }
defer sandbox.Destroy(ctx)
result, err := sandbox.Exec(ctx, mosaic.Shell("uname -a"), mosaic.ExecOptions{})
if err != nil { log.Fatal(err) }
fmt.Println(result.Stdout)
```

## Streaming

```go
stream, err := sandbox.ExecStream(ctx, mosaic.Shell("printf hello"), mosaic.ExecOptions{})
if err != nil { log.Fatal(err) }
defer stream.Close()
for stream.Next() {
	fmt.Println(stream.Event().Data)
}
if err := stream.Err(); err != nil { log.Fatal(err) }
```

## Files

```go
_, err = sandbox.Files().WriteString(ctx, "/workspace/hello.txt", "hello")
contents, err := sandbox.Files().ReadString(ctx, "/workspace/hello.txt")
fmt.Println(contents)
```

## Volumes and secrets

```go
secret, err := client.SetSecret(ctx, "API_KEY", "value")
_ = secret
volume, err := client.CreateVolume(ctx, "build-cache", "")
_ = volume
```

Values stored as organization secrets are never returned by the API.

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


## SDK 0.14.9 release verification

The module inputs match protected Mosaic Sandbox source `d0f239acf148831e026e54b05085d902f57fde26` (source preparation PR938). The synchronization is mirror commit `5339a11d11a2517d9d6790e59e8356c5fa084530` from PR11; all 11 module inputs have combined content digest `c596bc2b3d8d068c9308c3c8ee1c817c33d3ca3b94c32d78dd7b578920f7d6f4`. `go test ./...` passes. CPU metrics/evidence and workload-profile fields do not activate server policy.

The mirror tag and registry publication are held until this source-parity release record receives independent review. PR11 merged under the existing mirror branch rules before an independent approval was recorded; this verification does not claim that earlier approval existed. The final signed `v0.14.9` tag will include the exact `sdk-v0.14.9` source-tag provenance and be verified through the public Go proxy/checksum database before Python/npm publishing. Previous `v0.14.8` remains the consumer rollback.

## SDK 0.15.0 release candidate

Exact module inputs synchronized from reviewed protected Mosaic Sandbox main `2b92fb96980f02a19b30f2a2cf4fa6666566ef68` (PR983), including multi-key secret bundles, access-only consumer updates, private invocation inputs, service-scoped Connections and typed network-profile references. Stable/private activation remains gated by serving qualification; the SDK grants no route.

All 18 module inputs have combined content digest `48bd381dea8f95f8c050881f2fed3f5e8bfba58313c90a061b028110920561d4`. All input bytes match source; the mirror adds no implementation. Root LICENSE/NOTICE match the source. The signed mirror release will carry:

```text
Source: mosaicos-repos/mosaic-sandbox sdk-v0.15.0 (2b92fb96980f02a19b30f2a2cf4fa6666566ef68)
```

Independent review, source/mirror signed-tag verification, clean-cache public proxy/source parity and the governed publisher remain release gates. Existing v0.14.9 remains the consumer package rollback; gateway/schema/runtime rollback has separate compatibility requirements.
