package mosaic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

const metricsCPUAllocation = `{"requested_vcpu":2,"reserved_millicpus":1500,"burst_millicpus":250,"effective_millicpus":1750,"weight":100,"revision":4,"policy_version":"experimental-shared-v1","source":"adaptive"}`

const metricsCPUTelemetry = `{
	"accounting_scope":"vmm_cgroup","allocation":` + metricsCPUAllocation + `,
	"readback":{"quota_usec":175000,"period_usec":100000,"effective_millicpus":1750,"weight":100},
	"enforcement_matches":true,"needs_reconciliation":false,"attempted_revision":null,
	"stat":{"usage_usec":4000,"user_usec":3000,"system_usec":1000,"nr_periods":20,"nr_throttled":2,"throttled_usec":100},
	"pressure":{"some":{"avg10":1.5,"avg60":0.4,"avg300":0.1,"total_usec":100},"full":{"avg10":0.5,"avg60":0.2,"avg300":0.0,"total_usec":50}},
	"sampled_at_unix_ms":1800000000000,"read_duration_usec":20,"interval_usec":1000000,
	"stale":false,"counters_reset":false,
	"delta":{"usage_usec":0,"user_usec":0,"system_usec":0,"nr_periods":10,"nr_throttled":0,"throttled_usec":0},
	"usage_cores":0.0,"budget_utilization":0.0,"pressure_some_delta_usec":0,"pressure_full_delta_usec":0
}`

func readSandboxMetrics(testContext *testing.T, payload string) *SandboxMetrics {
	testContext.Helper()
	client := testClient(testContext, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/sandboxes/sbx/metrics" {
			testContext.Errorf("unexpected metrics request: %s %s", request.Method, request.URL.Path)
		}
		_, _ = io.WriteString(writer, payload)
	}))
	sandbox := &Sandbox{ID: "sbx", client: client}
	metrics, err := sandbox.Metrics(context.Background())
	if err != nil {
		testContext.Fatal(err)
	}
	return metrics
}

func TestSandboxMetricsCPUCompleteSample(testContext *testing.T) {
	metrics := readSandboxMetrics(testContext, `{"sandbox_id":"sbx","cpu":{"allocation":`+metricsCPUAllocation+`,"telemetry":`+metricsCPUTelemetry+`}}`)
	if metrics.CPU == nil || metrics.CPU.Telemetry == nil {
		testContext.Fatalf("CPU telemetry discarded: %#v", metrics)
	}
	allocation := metrics.CPU.Allocation
	if allocation != (CPUAllocation{RequestedVCPU: 2, ReservedMillicpus: 1500, BurstMillicpus: 250,
		EffectiveMillicpus: 1750, Weight: 100, Revision: 4, PolicyVersion: "experimental-shared-v1", Source: "adaptive"}) {
		testContext.Fatalf("allocation = %#v", allocation)
	}
	sample := metrics.CPU.Telemetry
	if sample.Allocation != allocation || sample.AccountingScope != "vmm_cgroup" ||
		sample.Readback != (CPUReadback{QuotaUsec: 175000, PeriodUsec: 100000, EffectiveMillicpus: 1750, Weight: 100}) ||
		!sample.EnforcementMatches || sample.NeedsReconciliation || sample.AttemptedRevision != nil ||
		sample.SampledAtUnixMS != 1800000000000 || sample.ReadDurationUsec != 20 ||
		sample.IntervalUsec == nil || *sample.IntervalUsec != 1000000 || sample.Stale || sample.CountersReset {
		testContext.Fatalf("sample metadata/readback = %#v", sample)
	}
	if sample.Stat != (CPUStat{UsageUsec: 4000, UserUsec: 3000, SystemUsec: 1000,
		NrPeriods: 20, NrThrottled: 2, ThrottledUsec: 100}) ||
		sample.Delta == nil || *sample.Delta != (CPUStat{NrPeriods: 10}) {
		testContext.Fatalf("CPU counters = %#v, delta = %#v", sample.Stat, sample.Delta)
	}
	if sample.Pressure.Some != (CPUPressureLine{Avg10: 1.5, Avg60: 0.4, Avg300: 0.1, TotalUsec: 100}) ||
		sample.Pressure.Full == nil || *sample.Pressure.Full != (CPUPressureLine{Avg10: 0.5, Avg60: 0.2, TotalUsec: 50}) {
		testContext.Fatalf("CPU pressure = %#v", sample.Pressure)
	}
	if sample.UsageCores == nil || *sample.UsageCores != 0 ||
		sample.BudgetUtilization == nil || *sample.BudgetUtilization != 0 ||
		sample.PressureSomeDeltaUsec == nil || *sample.PressureSomeDeltaUsec != 0 ||
		sample.PressureFullDeltaUsec == nil || *sample.PressureFullDeltaUsec != 0 {
		testContext.Fatalf("zero rates/deltas must remain distinct from unavailable: %#v", sample)
	}
}

func TestSandboxMetricsCPUFirstSampleNulls(testContext *testing.T) {
	var sample map[string]any
	if err := json.Unmarshal([]byte(metricsCPUTelemetry), &sample); err != nil {
		testContext.Fatal(err)
	}
	for _, name := range []string{"attempted_revision", "interval_usec", "delta", "usage_cores",
		"budget_utilization", "pressure_some_delta_usec", "pressure_full_delta_usec"} {
		sample[name] = nil
	}
	sample["pressure"].(map[string]any)["full"] = nil
	encoded, err := json.Marshal(sample)
	if err != nil {
		testContext.Fatal(err)
	}
	metrics := readSandboxMetrics(testContext, `{"cpu":{"allocation":`+metricsCPUAllocation+`,"telemetry":`+string(encoded)+`}}`)
	if metrics.CPU == nil || metrics.CPU.Telemetry == nil {
		testContext.Fatal("first CPU observation must not be discarded")
	}
	observed := metrics.CPU.Telemetry
	if observed.AttemptedRevision != nil || observed.IntervalUsec != nil || observed.Delta != nil ||
		observed.UsageCores != nil || observed.BudgetUtilization != nil ||
		observed.PressureSomeDeltaUsec != nil || observed.PressureFullDeltaUsec != nil || observed.Pressure.Full != nil {
		testContext.Fatalf("unavailable telemetry must not turn into zero: %#v", observed)
	}
}

func TestSandboxMetricsCPUAbsentOrUnavailable(testContext *testing.T) {
	for _, scenario := range []struct {
		name         string
		payload      string
		present      bool
		errorMessage string
	}{
		{name: "absent", payload: `{"sandbox_id":"sbx"}`},
		{name: "no-controller", payload: `{"cpu":{"allocation":` + metricsCPUAllocation + `,"telemetry":null}}`, present: true},
		{name: "sampling-error", payload: `{"cpu":{"allocation":` + metricsCPUAllocation + `,"telemetry_error":"CPU observation unavailable"}}`, present: true, errorMessage: "CPU observation unavailable"},
	} {
		testContext.Run(scenario.name, func(testContext *testing.T) {
			metrics := readSandboxMetrics(testContext, scenario.payload)
			if (metrics.CPU != nil) != scenario.present {
				testContext.Fatalf("unexpected CPU presence: %#v", metrics.CPU)
			}
			if !scenario.present {
				return
			}
			if metrics.CPU.Allocation.ReservedMillicpus != 1500 || metrics.CPU.Telemetry != nil {
				testContext.Fatalf("missing sample discarded allocation or fabricated telemetry: %#v", metrics.CPU)
			}
			if scenario.errorMessage == "" {
				if metrics.CPU.TelemetryError != nil {
					testContext.Fatalf("unexpected telemetry error: %s", *metrics.CPU.TelemetryError)
				}
			} else if metrics.CPU.TelemetryError == nil || *metrics.CPU.TelemetryError != scenario.errorMessage {
				testContext.Fatalf("telemetry error discarded: %#v", metrics.CPU)
			}
		})
	}
}
