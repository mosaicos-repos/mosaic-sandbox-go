package mosaic_test

import (
	"context"
	"fmt"
	"log"

	mosaic "github.com/mosaicos-repos/mosaic-sandbox-go"
)

// This mirrors the Functions section of README.md. It has no Output comment, so
// `go test` compiles it without running it, and a README example that stops
// compiling fails the build.
func ExampleClient_Function() {
	client, err := mosaic.New()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	thumbnail := client.Function(mosaic.Argv("python", "-m", "thumbnail"), mosaic.FunctionOptions{
		Template:     "python-3.11",
		Secrets:      []string{"OBJECT_STORE_TOKEN"},
		NetworkAllow: []string{"objects.mosaicos.com:443"},
		TimeoutMs:    60000,
	})

	result, err := thumbnail.InvokeWith(ctx, mosaic.InvocationOverrides{
		Env: map[string]string{"OBJECT_KEY": "images/input.jpg"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(result.Stdout, result.SandboxDestroyed)
}
