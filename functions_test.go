package mosaic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

type recordedRun struct {
	method string
	path   string
	key    string
	body   map[string]any
}

func runRecorder(t *testing.T) (*Client, *[]recordedRun) {
	t.Helper()
	var calls []recordedRun
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, recordedRun{method: r.Method, path: r.URL.Path, key: r.Header.Get("Idempotency-Key"), body: body})
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"sandbox_id":"sbx-fn","exit_code":0,"sandbox_destroyed":true}`)
	}))
	return client, &calls
}

func str(value string) *string { return &value }

func num(value int) *int { return &value }

func thumbnailFunction(client *Client) *Function {
	return client.Function(Argv("python", "-m", "thumbnail"), FunctionOptions{
		Template:     "python-3.11",
		Secrets:      []string{"OBJECT_STORE_TOKEN"},
		NetworkAllow: []string{"objects.mosaicos.com:443"},
		NetworkDeny:  []string{"10.0.0.0/8"},
		TimeoutMs:    60000,
	})
}

func TestDefiningAFunctionAsksTheServiceForNothing(t *testing.T) {
	client, calls := runRecorder(t)
	thumbnailFunction(client)
	if len(*calls) != 0 {
		t.Fatalf("defining a function made %d requests", len(*calls))
	}
}

func TestFunctionInvocationIsOneRunTheServiceCleansUpAfter(t *testing.T) {
	client, calls := runRecorder(t)
	result, err := thumbnailFunction(client).Invoke(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.SandboxID != "sbx-fn" || !result.SandboxDestroyed {
		t.Fatalf("result = %#v", result)
	}
	if len(*calls) != 1 {
		t.Fatalf("invocation made %d requests", len(*calls))
	}
	call := (*calls)[0]
	if call.method != http.MethodPost || call.path != "/v1/run" {
		t.Fatalf("request = %s %s", call.method, call.path)
	}
	if _, kept := call.body["keep_sandbox"]; kept {
		t.Fatalf("invocation asked to keep the sandbox: %#v", call.body)
	}
}

func TestFunctionSpecificationCarriesSecretsNetworkPolicyAndTimeout(t *testing.T) {
	client, calls := runRecorder(t)
	if _, err := thumbnailFunction(client).Invoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := (*calls)[0].body
	if body["template"] != "python-3.11" || body["timeout_ms"] != float64(60000) {
		t.Fatalf("body = %#v", body)
	}
	if !reflect.DeepEqual(body["secrets"], []any{"OBJECT_STORE_TOKEN"}) {
		t.Fatalf("secrets = %#v", body["secrets"])
	}
	network, ok := body["network"].(map[string]any)
	if !ok || !reflect.DeepEqual(network["allow"], []any{"objects.mosaicos.com:443"}) || !reflect.DeepEqual(network["deny"], []any{"10.0.0.0/8"}) {
		t.Fatalf("network = %#v", body["network"])
	}
	if !reflect.DeepEqual(body["argv"], []any{"python", "-m", "thumbnail"}) {
		t.Fatalf("argv = %#v", body["argv"])
	}
}

func TestFunctionInvocationKeyReachesTheDeduplicatingRoute(t *testing.T) {
	client, calls := runRecorder(t)
	function := thumbnailFunction(client)
	if _, err := function.InvokeWith(context.Background(), InvocationOverrides{IdempotencyKey: "k-thumbnail-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := function.Invoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].key != "k-thumbnail-1" {
		t.Fatalf("idempotency key = %q", (*calls)[0].key)
	}
	if (*calls)[1].key == "k-thumbnail-1" {
		t.Fatal("a later invocation reused the previous invocation's key")
	}
}

func TestASpecificationHasNowhereToPutWhatBelongsToOneCall(t *testing.T) {
	// A key identifies one invocation and a kept sandbox is one run's, so
	// FunctionOptions spells the create and exec fields out instead of embedding
	// RunOnceOptions: neither option can be written on a specification at all.
	// The rest of a run's fields must stay sayable, hence the comparison.
	specification := reflect.TypeFor[FunctionOptions]()
	for _, perCall := range []string{"IdempotencyKey", "KeepSandbox"} {
		if _, found := specification.FieldByName(perCall); found {
			t.Fatalf("a function specification can be given %s", perCall)
		}
	}

	run := reflect.TypeFor[RunOnceOptions]()
	for _, shared := range []reflect.Type{reflect.TypeFor[CreateOptions](), reflect.TypeFor[ExecOptions]()} {
		for index := range shared.NumField() {
			field := shared.Field(index)
			if field.Name == "IdempotencyKey" {
				continue
			}
			if specified, found := specification.FieldByName(field.Name); !found || specified.Type != field.Type {
				t.Fatalf("a specification cannot say %s, which one run can", field.Name)
			}
		}
	}
	for index := range specification.NumField() {
		field := specification.Field(index)
		if inherited, found := run.FieldByName(field.Name); !found || inherited.Type != field.Type {
			t.Fatalf("a specification says %s, which one run does not", field.Name)
		}
	}
}

func TestASpecificationCannotKeepTheSandboxItsInvocationsCreate(t *testing.T) {
	// Kept once, kept every invocation: a specification that asks for it leaks a
	// sandbox per call, which is the one thing the one-shot route exists to avoid.
	// FunctionOptions has no KeepSandbox field, so this is what the surface can say
	// at all; RunOnce still takes the option for a single run.
	client, calls := runRecorder(t)
	function := client.Function(Argv("python", "-m", "thumbnail"), FunctionOptions{
		Template: "python-3.11",
	})
	result, err := function.Invoke(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := (*calls)[0].body["keep_sandbox"]; kept {
		t.Fatalf("invocation asked to keep the sandbox: %#v", (*calls)[0].body)
	}
	if !result.SandboxDestroyed {
		t.Fatalf("result = %#v", result)
	}
}

func TestAnInvocationCanClearWhatTheSpecificationDefaults(t *testing.T) {
	client, calls := runRecorder(t)
	function := client.Function(Argv("python", "-m", "thumbnail"), FunctionOptions{
		Template: "python-3.11",
		Cwd:      "/spec",
		Stdin:    "spec",
	})
	if _, err := function.InvokeWith(context.Background(), InvocationOverrides{Cwd: str(""), Stdin: str("")}); err != nil {
		t.Fatal(err)
	}
	if _, err := function.Invoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cwd", "stdin"} {
		if value, present := (*calls)[0].body[field]; present {
			t.Fatalf("cleared %s was sent as %#v", field, value)
		}
	}
	if (*calls)[1].body["cwd"] != "/spec" || (*calls)[1].body["stdin"] != "spec" {
		t.Fatalf("clearing outlived its invocation: %#v", (*calls)[1].body)
	}
}

func TestChangingTheCallersCollectionsDoesNotChangeTheSpecification(t *testing.T) {
	client, calls := runRecorder(t)
	argv := []string{"python", "-m", "thumbnail"}
	secrets := []string{"OBJECT_STORE_TOKEN"}
	env := map[string]string{"OBJECT_KEY": "one.jpg"}
	function := client.Function(Command{Argv: argv}, FunctionOptions{
		Template: "python-3.11",
		Secrets:  secrets,
		Env:      env,
	})

	argv[2] = "other"
	secrets[0] = "OTHER_TOKEN"
	env["OBJECT_KEY"] = "two.jpg"

	if _, err := function.Invoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := (*calls)[0].body
	if !reflect.DeepEqual(body["argv"], []any{"python", "-m", "thumbnail"}) {
		t.Fatalf("argv = %#v", body["argv"])
	}
	if !reflect.DeepEqual(body["secrets"], []any{"OBJECT_STORE_TOKEN"}) {
		t.Fatalf("secrets = %#v", body["secrets"])
	}
	if !reflect.DeepEqual(body["env"], map[string]any{"OBJECT_KEY": "one.jpg"}) {
		t.Fatalf("env = %#v", body["env"])
	}
}

func TestInvokingLeavesTheFunctionSpecificationUnchanged(t *testing.T) {
	client, calls := runRecorder(t)
	function := thumbnailFunction(client)

	if _, err := function.InvokeWith(context.Background(), InvocationOverrides{
		Cwd:       str("/workspace"),
		Env:       map[string]string{"OBJECT_KEY": "one.jpg"},
		Stdin:     str("one"),
		TimeoutMs: num(9000),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := function.Invoke(context.Background()); err != nil {
		t.Fatal(err)
	}

	first, second := (*calls)[0].body, (*calls)[1].body
	if first["cwd"] != "/workspace" || first["stdin"] != "one" || first["timeout_ms"] != float64(9000) {
		t.Fatalf("overridden invocation = %#v", first)
	}
	if second["timeout_ms"] != float64(60000) {
		t.Fatalf("second invocation inherited an override: %#v", second)
	}
	for _, field := range []string{"cwd", "stdin", "env"} {
		if _, present := second[field]; present {
			t.Fatalf("second invocation inherited %q: %#v", field, second)
		}
	}
	if second["template"] != "python-3.11" || !reflect.DeepEqual(second["secrets"], []any{"OBJECT_STORE_TOKEN"}) {
		t.Fatalf("specification changed: %#v", second)
	}
}
