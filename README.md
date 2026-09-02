# Mosaic Sandbox Go SDK

The Go SDK uses only the standard library and supports Go 1.22 or newer.

## Installation

Install the tagged module through the Go proxy:

```bash
go get github.com/mosaicos-repos/mosaic-sandbox-go@v0.14.0
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
