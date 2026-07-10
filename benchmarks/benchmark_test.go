package benchmark

import (
	"os"
	"testing"
	"time"
)

func TestHarnessBasic(t *testing.T) {
	h := NewHarness()
	h.Start()

	// Simulate 100 operations
	for i := 0; i < 100; i++ {
		h.TimedOp(func() {
			time.Sleep(10 * time.Microsecond)
		})
	}

	result := h.Finish("test_benchmark", "Unit test of harness", Workload{
		EntityCount:  100,
		UpdateRateHz: 10,
	})

	if result.Name != "test_benchmark" {
		t.Errorf("expected name 'test_benchmark', got '%s'", result.Name)
	}
	if result.Workload.Iterations != 100 {
		t.Errorf("expected 100 iterations, got %d", result.Workload.Iterations)
	}
	if result.Latency.P50us <= 0 {
		t.Errorf("expected positive P50, got %.1f", result.Latency.P50us)
	}
	if result.Throughput <= 0 {
		t.Errorf("expected positive throughput, got %.1f", result.Throughput)
	}

	// Test JSON export
	tmpFile := "/tmp/streamgrid_bench_test.json"
	err := result.SaveJSON(tmpFile)
	if err != nil {
		t.Fatalf("SaveJSON failed: %v", err)
	}
	defer os.Remove(tmpFile)

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("JSON output is empty")
	}

	result.PrintSummary()
}

func TestLatencyPercentiles(t *testing.T) {
	h := NewHarness()
	h.Start()

	// Insert known durations
	for i := 1; i <= 1000; i++ {
		h.RecordSample(time.Duration(i) * time.Microsecond)
	}

	result := h.Finish("percentile_test", "Test percentile accuracy", Workload{})

	// P50 should be ~500μs
	if result.Latency.P50us < 490 || result.Latency.P50us > 510 {
		t.Errorf("P50 expected ~500μs, got %.1fμs", result.Latency.P50us)
	}
	// P99 should be ~990μs
	if result.Latency.P99us < 980 || result.Latency.P99us > 1000 {
		t.Errorf("P99 expected ~990μs, got %.1fμs", result.Latency.P99us)
	}
}
