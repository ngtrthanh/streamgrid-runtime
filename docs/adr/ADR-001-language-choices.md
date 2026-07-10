# ADR-001: Language Choices

**Status:** Accepted
**Date:** 2026-07-09
**Authors:** StreamGrid Team

## Problem

Which programming languages should be used for the StreamGrid runtime components?
The system spans backend services, protocol handling, GPU compute, and browser rendering.

## Alternatives

### Option A: All Go

Single language for backend, protocol, compute. Browser via WASM compilation.

### Option B: All Rust

Single language for everything. Browser via wasm-bindgen.

### Option C: Go + Rust + TypeScript (Hybrid)

- Go for backend services, generators, edge servers (fast iteration, excellent concurrency)
- Rust for protocol encoding, compute-critical paths (zero-cost abstractions, memory control)
- TypeScript for browser client, workers, renderer coordination

## Tradeoffs

| Criterion         | All Go     | All Rust   | Hybrid (Go+Rust+TS) |
|-------------------|------------|------------|----------------------|
| Development speed | ★★★★★      | ★★☆☆☆      | ★★★★☆                |
| Memory control    | ★★☆☆☆      | ★★★★★      | ★★★★☆                |
| Concurrency model | ★★★★★      | ★★★★☆      | ★★★★★                |
| Browser native    | ★★☆☆☆      | ★★★☆☆      | ★★★★★                |
| Zero-copy paths   | ★★☆☆☆      | ★★★★★      | ★★★★☆                |
| Ecosystem/libs    | ★★★★☆      | ★★★☆☆      | ★★★★★                |
| Team scalability  | ★★★★☆      | ★★☆☆☆      | ★★★★☆                |
| WASM performance  | ★★★☆☆      | ★★★★★      | ★★★★☆                |

## Benchmark Evidence

Not applicable for this architectural decision — this is a qualitative tradeoff analysis.
Performance-critical decisions within each language will be benchmarked in subsequent ADRs.

## Decision

**Hybrid: Go + Rust + TypeScript**

Rationale:
1. **Go** — Backend services benefit from goroutines, fast compilation, and simple deployment.
   The generator and edge servers are IO-bound and benefit from Go's concurrency model.
2. **Rust** — Protocol encoding/decoding and spatial compute are CPU-bound and benefit from
   zero-cost abstractions, `repr(C)` layout control, and bytemuck for zero-copy casting.
3. **TypeScript** — Browser is the primary rendering target. Native Web APIs (WebTransport,
   SharedArrayBuffer, WebGPU) are most naturally accessed from TypeScript/JavaScript.

## Future Revisions

- If Rust WASM proves fast enough for all browser paths, TypeScript may be reduced to glue code.
- If Go's GC proves problematic for the edge server hot path, that component may migrate to Rust.
- If a fourth language (C/Zig) is needed for SIMD-critical DSP, a new ADR will be created.
