// Package benchmark provides a standard framework for StreamGrid benchmarks.
//
// Every benchmark reports hardware, software, workload parameters, and
// latency percentiles (P50, P95, P99, P999) as required by the project spec.
package benchmark

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"time"
)

// Result captures a complete benchmark result with all required metadata.
type Result struct {
	// Metadata
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Timestamp   time.Time `json:"timestamp"`

	// Environment
	Environment Environment `json:"environment"`

	// Workload parameters
	Workload Workload `json:"workload"`

	// Results
	Throughput  float64            `json:"throughput_ops_per_sec"`
	Latency     LatencyResult      `json:"latency"`
	Resources   ResourceUsage      `json:"resources"`
	CustomStats map[string]float64 `json:"custom_stats,omitempty"`
}

// Environment captures hardware and software context.
type Environment struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	CPUs         int    `json:"cpus"`
	GoVersion    string `json:"go_version"`
	RustVersion  string `json:"rust_version,omitempty"`
	Hostname     string `json:"hostname"`
	TotalMemoryMB int64 `json:"total_memory_mb,omitempty"`
}

// Workload describes what was benchmarked.
type Workload struct {
	EntityCount int     `json:"entity_count"`
	UpdateRateHz float64 `json:"update_rate_hz"`
	DurationSec  float64 `json:"duration_sec"`
	Iterations   int64   `json:"iterations"`
	InputSizeBytes int64 `json:"input_size_bytes,omitempty"`
}

// LatencyResult reports latency percentiles in microseconds.
type LatencyResult struct {
	P50us  float64 `json:"p50_us"`
	P95us  float64 `json:"p95_us"`
	P99us  float64 `json:"p99_us"`
	P999us float64 `json:"p999_us"`
	MinUs  float64 `json:"min_us"`
	MaxUs  float64 `json:"max_us"`
	MeanUs float64 `json:"mean_us"`
}

// ResourceUsage reports CPU, RAM, and other resource consumption.
type ResourceUsage struct {
	CPUPercent     float64 `json:"cpu_percent,omitempty"`
	RAMBytes       int64   `json:"ram_bytes,omitempty"`
	AllocsPerOp    int64   `json:"allocs_per_op,omitempty"`
	BytesPerOp     int64   `json:"bytes_per_op,omitempty"`
	GoroutineCount int     `json:"goroutine_count,omitempty"`
}

// Harness runs benchmarks and collects results.
type Harness struct {
	samples []time.Duration
	start   time.Time
	env     Environment
}

// NewHarness creates a new benchmark harness with environment detection.
func NewHarness() *Harness {
	hostname, _ := os.Hostname()
	return &Harness{
		env: Environment{
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			CPUs:      runtime.NumCPU(),
			GoVersion: runtime.Version(),
			Hostname:  hostname,
		},
	}
}

// Start begins timing.
func (h *Harness) Start() {
	h.start = time.Now()
	h.samples = h.samples[:0]
}

// RecordSample records a single latency sample.
func (h *Harness) RecordSample(d time.Duration) {
	h.samples = append(h.samples, d)
}

// TimedOp executes fn and records its duration as a sample.
func (h *Harness) TimedOp(fn func()) {
	start := time.Now()
	fn()
	h.RecordSample(time.Since(start))
}

// Finish computes the result from collected samples.
func (h *Harness) Finish(name, description string, workload Workload) Result {
	latency := computeLatency(h.samples)
	totalDuration := time.Since(h.start)

	throughput := 0.0
	if totalDuration > 0 {
		throughput = float64(len(h.samples)) / totalDuration.Seconds()
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	workload.DurationSec = totalDuration.Seconds()
	workload.Iterations = int64(len(h.samples))

	return Result{
		Name:        name,
		Description: description,
		Timestamp:   time.Now(),
		Environment: h.env,
		Workload:    workload,
		Throughput:  throughput,
		Latency:     latency,
		Resources: ResourceUsage{
			RAMBytes:       int64(mem.Alloc),
			AllocsPerOp:    int64(mem.Mallocs) / int64(max(len(h.samples), 1)),
			GoroutineCount: runtime.NumGoroutine(),
		},
	}
}

func computeLatency(samples []time.Duration) LatencyResult {
	if len(samples) == 0 {
		return LatencyResult{}
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	sum := time.Duration(0)
	for _, s := range samples {
		sum += s
	}

	return LatencyResult{
		P50us:  percentile(samples, 0.50),
		P95us:  percentile(samples, 0.95),
		P99us:  percentile(samples, 0.99),
		P999us: percentile(samples, 0.999),
		MinUs:  float64(samples[0].Microseconds()),
		MaxUs:  float64(samples[len(samples)-1].Microseconds()),
		MeanUs: float64(sum.Microseconds()) / float64(len(samples)),
	}
}

func percentile(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx].Microseconds())
}

// SaveJSON writes the result to a JSON file.
func (r *Result) SaveJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// PrintSummary prints a human-readable benchmark summary.
func (r *Result) PrintSummary() {
	fmt.Printf("=== %s ===\n", r.Name)
	fmt.Printf("  %s\n", r.Description)
	fmt.Printf("  Environment: %s/%s, %d CPUs, %s\n", r.Environment.OS, r.Environment.Arch, r.Environment.CPUs, r.Environment.GoVersion)
	fmt.Printf("  Workload: %d entities, %.1f Hz, %d iterations, %.2fs\n",
		r.Workload.EntityCount, r.Workload.UpdateRateHz, r.Workload.Iterations, r.Workload.DurationSec)
	fmt.Printf("  Throughput: %.0f ops/s\n", r.Throughput)
	fmt.Printf("  Latency: P50=%.1fμs P95=%.1fμs P99=%.1fμs P999=%.1fμs\n",
		r.Latency.P50us, r.Latency.P95us, r.Latency.P99us, r.Latency.P999us)
	fmt.Printf("  Latency: min=%.1fμs max=%.1fμs mean=%.1fμs\n",
		r.Latency.MinUs, r.Latency.MaxUs, r.Latency.MeanUs)
	fmt.Printf("  Resources: RAM=%d KB, goroutines=%d\n",
		r.Resources.RAMBytes/1024, r.Resources.GoroutineCount)
	fmt.Println()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
