package mosaic

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestBundleManagementAndVersion(t *testing.T) {
	var method, version string
	var body map[string]any
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		version = r.URL.Query().Get("version")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SecretBundleInfo{Name: "service", Namespace: "prod", Version: 2, Keys: []string{"TOKEN"}})
	}))
	if _, err := client.WriteSecretBundle(context.Background(), "prod", "service", map[string]string{"TOKEN": "synthetic"}, SecretBundleWriteOptions{Mode: "merge"}); err == nil {
		t.Fatal("accepted an update without a version")
	}
	result, err := client.WriteSecretBundle(context.Background(), "prod", "service", map[string]string{"TOKEN": "synthetic"}, SecretBundleWriteOptions{Mode: "merge", Version: 1, Consumers: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPatch || version != "1" || result.Version != 2 {
		t.Fatal("wrong conditional update contract")
	}
	if !reflect.DeepEqual(body["consumers"], []any{}) {
		t.Fatal("explicit empty grants were not retained")
	}
}

func TestBundleReferencesAndFunctionCopies(t *testing.T) {
	options := RunOnceOptions{CreateOptions: CreateOptions{Secrets: []string{"LEGACY"}, SecretBundles: []SecretReference{{Name: "service", Namespace: "prod", RequiredKeys: []string{"TOKEN"}}}}}
	copied := cloneRunOnceOptions(options)
	options.SecretBundles[0].RequiredKeys[0] = "CHANGED"
	options.SecretBundles[0].Namespace = "dev"
	encoded, err := json.Marshal(createBody(copied.CreateOptions))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	expected := []any{"LEGACY", map[string]any{"name": "service", "namespace": "prod", "required_keys": []any{"TOKEN"}}}
	if !reflect.DeepEqual(body["secrets"], expected) {
		t.Fatal("bundle reference or immutable function options changed")
	}
}

func TestBundleAccessOnlyDoesNotSendValues(t *testing.T) {
	for _, consumers := range [][]string{{}, {"service-key"}} {
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if _, present := body["values"]; present || len(body) != 1 || r.Method != http.MethodPatch || r.URL.Query().Get("version") != "1" {
				t.Fatal("access update must send only explicit consumers with a version")
			}
			expected := make([]any, len(consumers))
			for i, item := range consumers {
				expected[i] = item
			}
			if !reflect.DeepEqual(body["consumers"], expected) {
				t.Fatal("consumer replacement changed")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(SecretBundleInfo{Name: "service", Namespace: "prod", Version: 2, Keys: []string{"TOKEN"}})
		}))
		if _, err := client.WriteSecretBundle(context.Background(), "prod", "service", nil,
			SecretBundleWriteOptions{Mode: "merge", Version: 1, Consumers: consumers}); err != nil {
			t.Fatal(err)
		}
	}
}
