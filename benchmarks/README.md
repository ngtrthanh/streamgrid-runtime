# Benchmarks

Reproducible benchmark suite for StreamGrid Runtime.

## Requirements

Every benchmark must report:

- Hardware (CPU, RAM, GPU, NIC)
- Software versions (OS, compiler, runtime)
- Compiler/build options
- Workload description
- Input size
- Throughput
- Latency: P50, P95, P99, P999
- CPU utilization
- RAM usage
- GPU utilization (if applicable)
- Bandwidth
- Power consumption (if measurable)

## Subdirectories

- **datasets/** — Input data for benchmarks (synthetic and recorded)
- **results/** — Benchmark output data, charts, analysis
- **replay/** — Replay tools for deterministic benchmark reproduction
