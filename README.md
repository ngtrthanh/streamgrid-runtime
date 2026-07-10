# StreamGrid Runtime

A reusable runtime for large-scale real-time spatial telemetry.

Browser-native, zero-copy, low-latency — capable of distributing and rendering
hundreds of thousands of continuously moving entities with minimal CPU usage.

## Vision

Create a reference architecture for browser-native, low-latency, large-scale
spatial telemetry systems through rigorous engineering, measurement, and open
scientific methodology.

## Architecture

```
SDR → DSP Decoder → Canonical State → Spatial Index → Interest Management
    → Binary Encoder → WebTransport → Browser Worker → SharedArrayBuffer → GPU Renderer
```

## Research Questions

| ID  | Question |
|-----|----------|
| RQ1 | Can telemetry be transported from SDR to GPU with minimal copying? |
| RQ2 | Can WebTransport outperform WebSocket for massive telemetry fanout? |
| RQ3 | Can SharedArrayBuffer eliminate browser GC stalls? |
| RQ4 | Can GPU compute accelerate telemetry preprocessing? |
| RQ5 | What is the optimal binary transport format? |
| RQ6 | How should moving entities be partitioned spatially for efficient pub/sub? |
| RQ7 | What architecture sustains very large concurrent browser audiences on commodity hardware? |

## Tech Stack

- **Go** (latest stable) — backend, generator, edge servers
- **Rust** (latest stable) — protocol, compute-critical paths
- **TypeScript** — browser client, workers, renderer
- **WebGPU** — GPU rendering
- **WebTransport** — low-latency streaming
- **Docker Compose** — development environment

## Project Structure

```
streamgrid/
├── docs/           # Architecture, research, ADRs, papers
├── benchmarks/     # Datasets, results, replay tools
├── generator/      # Synthetic telemetry generator
├── protocol/       # Binary protocol encoder/decoder
├── edge/           # Edge servers, WebTransport
├── backend/        # Core backend services
├── compute/        # GPU compute, spatial indexing
├── renderer/       # WebGPU renderer
├── web/            # Browser client, workers
├── tools/          # Development tools
├── scripts/        # Automation scripts
├── examples/       # Usage examples
└── applications/   # Domain-specific applications
```

## Guiding Principles

1. Measure before optimizing
2. Every design decision must be benchmarked
3. Simplicity over cleverness
4. Zero-copy whenever practical
5. Browser is a first-class compute platform
6. Architecture must be reusable across domains
7. Every subsystem must be independently testable
8. Every experiment must be reproducible

## License

MIT
