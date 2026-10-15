package mosaic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvocationSecretsRedactAndImport(t *testing.T) {
	secret := "synthetic-private-input-20261014"
	source := map[string]string{"TOKEN": secret}
	input, err := NewInvocationSecrets(source)
	if err != nil {
		t.Fatal(err)
	}
	source["TOKEN"] = "changed"
	if input.wireValues()["TOKEN"] != secret {
		t.Fatal("input not copied")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, display := range []string{string(encoded), fmt.Sprintf("%v", input), fmt.Sprintf("%#v", input)} {
		if strings.Contains(display, secret) {
			t.Fatal("values exposed")
		}
	}
	parsed, err := InvocationSecretsFromJSON(strings.NewReader("{\"TOKEN\":\"" + secret + "\"}"))
	if err != nil || parsed.wireValues()["TOKEN"] != secret {
		t.Fatal(err)
	}
	parsed, err = InvocationSecretsFromDotenv(strings.NewReader("TOKEN='$(must-not-run)'\nURL=\"literal-$VAR\"\n"))
	if err != nil || parsed.wireValues()["TOKEN"] != "$(must-not-run)" {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE_FIXTURE", secret)
	if _, err = InvocationSecretsFromEnvironment([]string{"PRIVATE_FIXTURE"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]string{{}, {"TOKEN": ""}, {"MOSAIC_BAD": secret}, {"TOKEN": strings.Repeat("x", 32769)}} {
		if _, err = NewInvocationSecrets(bad); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestPrivateInvocationNoRedirectReplayOrProviderText(t *testing.T) {
	secret := "synthetic-private-input-20261014"
	input, _ := NewInvocationSecrets(map[string]string{"TOKEN": secret})
	calls := 0
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/run/ephemeral" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("wrong private route/replay")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["invocation_secrets"].(map[string]any)["TOKEN"] != secret {
			t.Error("private values not delivered")
		}
		w.Header().Set("Content-Type", "application/json")
		if status == 302 {
			w.Header().Set("Location", "/unexpected")
		}
		w.WriteHeader(status)
		if status == 200 {
			w.Write([]byte("{\"stdout\":\"ready\",\"stderr\":\"\",\"exit_code\":0,\"sandbox_id\":\"fixture\",\"sandbox_destroyed\":true,\"invocation_secrets_guest_readable\":true}"))
		} else {
			json.NewEncoder(w).Encode(map[string]string{"message": secret})
		}
	}))
	defer server.Close()
	client, err := New(WithEndpoint(server.URL), WithAPIToken("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := client.RunOnce(context.Background(), Shell("echo ready"), RunOnceOptions{InvocationSecrets: input})
	if err != nil || reply.Stdout != "ready" || !reply.InvocationSecretsGuestReadable {
		t.Fatal(err)
	}
	for _, failure := range []int{302, 429, 500} {
		status = failure
		_, err = client.RunOnce(context.Background(), Shell("echo ready"), RunOnceOptions{InvocationSecrets: input})
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("provider text exposed", err)
		}
	}
	if calls != 4 {
		t.Fatal("private invocation retried or redirected", calls)
	}
	_, err = client.RunOnce(context.Background(), Shell("echo ready"), RunOnceOptions{InvocationSecrets: input, KeepSandbox: true})
	if err == nil || calls != 4 {
		t.Fatal("retention attempted")
	}
}
func TestPrivateInvocationOverridesDoNotEnterFunctionSpec(t *testing.T) {
	input, _ := NewInvocationSecrets(map[string]string{"TOKEN": "synthetic-private-input-20261014"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["invocation_secrets"] == nil {
			t.Error("invocation input omitted")
		}
		w.Write([]byte("{\"stdout\":\"ready\",\"stderr\":\"\",\"exit_code\":0,\"sandbox_id\":\"fixture\",\"sandbox_destroyed\":true}"))
	}))
	defer server.Close()
	client, _ := New(WithEndpoint(server.URL), WithAPIToken("synthetic"))
	fn := client.Function(Shell("echo ready"), FunctionOptions{Template: "node-20"})
	if _, err := fn.InvokeWith(context.Background(), InvocationOverrides{InvocationSecrets: input}); err != nil {
		t.Fatal(err)
	}
	if fn.options.InvocationSecrets != nil {
		t.Fatal("private values retained in Function")
	}
}
