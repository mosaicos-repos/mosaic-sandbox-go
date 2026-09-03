package mosaic_test

import (
	"context"
	"log"

	mosaic "github.com/mosaicos-repos/mosaic-sandbox-go"
)

// This mirrors the Long-running attempts section of README.md. It has no
// Output comment, so go test compiles the recovery pattern without making a
// live request.
func ExampleClient_longRunningAttempt() {
	client, err := mosaic.New()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	ttlSeconds := 86400
	sandbox, err := client.CreateSandbox(ctx, mosaic.CreateOptions{
		Template: "base", TTLSeconds: &ttlSeconds,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sandbox.Destroy(ctx)

	started, err := sandbox.Processes().Start(
		ctx,
		mosaic.Argv("python", "-m", "train"),
		mosaic.StartOptions{},
	)
	if err != nil {
		log.Fatal(err)
	}
	sandboxID, processID := sandbox.ID, started.ID

	// A later client reconstructs both handles from the persisted IDs.
	sandbox, err = client.ConnectSandbox(ctx, sandboxID)
	if err != nil {
		log.Fatal(err)
	}
	processes, err := sandbox.Processes().List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var handle *mosaic.Process
	for _, process := range processes {
		if process.ID == processID {
			handle = process
			break
		}
	}
	if handle == nil {
		log.Fatal("process not found")
	}
	result, err := handle.Wait(ctx)
	if err != nil || !result.Success {
		log.Fatal("attempt failed")
	}
}
