package mosaic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const runtimeAttestationJSON = `{"schema_version":1,"origin":null,"last_transition":{"receipt_id":"11111111-1111-4111-8111-111111111111","runtime_id":"22222222-2222-4222-8222-222222222222","operation":"create","path":"warm_reuse","admission_profile":"computesdk","requested_shape":{"vcpu":null,"memory_mb":null},"resolved_shape":{"vcpu":4,"memory_mb":8192},"observed_shape":{"vcpu":4,"memory_mb":8192},"observed_at_ms":1800000000000,"cpu":null}}`

func TestCreateRetainsRuntimeAttestation(testContext *testing.T) {
	client := testClient(testContext, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil || strings.Contains(string(body), "runtime_attestation") {
			testContext.Fatalf("unexpected create evidence input: %v", err)
		}
		_, _ = io.WriteString(writer, `{"id":"sandbox","runtime_attestation":`+runtimeAttestationJSON+`}`)
	}))
	sandbox, err := client.CreateSandbox(context.Background(), CreateOptions{})
	if err != nil {
		testContext.Fatal(err)
	}
	if sandbox.RuntimeAttestation == nil || sandbox.RuntimeAttestation.Origin != nil ||
		sandbox.RuntimeAttestation.LastTransition.ObservedShape.VCPU != 4 ||
		sandbox.RuntimeAttestation.LastTransition.Path != "warm_reuse" {
		testContext.Fatalf("runtime evidence lost: %#v", sandbox.RuntimeAttestation)
	}
}

func TestDemandEvidenceNullFalseTrueRemainDistinct(testContext *testing.T) {
	for _, value := range []string{"null", "false", "true"} {
		testContext.Run(value, func(testContext *testing.T) {
			var metrics SandboxMetrics
			payload := `{"runtime_attestation":` + runtimeAttestationJSON + `,"cpu":{"telemetry":{"demand_evidence":{"schema_version":1,"scope":"single_interval_full_vmm_not_host_capacity_or_promotion_eligibility","allocation_revision":3,"sampled_at_unix_ms":1800000000000,"constrained":` + value + `,"reasons":[],"interval_usec":null,"usage_millicpus":null,"throttled_usec":null,"throttled_periods":null,"pressure_some_delta_usec":null,"pressure_full_delta_usec":null}}}}`
			if err := json.Unmarshal([]byte(payload), &metrics); err != nil {
				testContext.Fatal(err)
			}
			evidence := metrics.CPU.Telemetry.DemandEvidence
			if evidence == nil || evidence.AllocationRevision != 3 || evidence.PressureFullDeltaUsec != nil {
				testContext.Fatalf("sample evidence lost: %#v", evidence)
			}
			if value == "null" && evidence.Constrained != nil || value != "null" &&
				(evidence.Constrained == nil || *evidence.Constrained != (value == "true")) {
				testContext.Fatalf("constraint state changed: %#v", evidence.Constrained)
			}
		})
	}
}
