# ADR-003: Entity Model Design

**Status:** Accepted
**Date:** 2026-07-09
**Authors:** StreamGrid Team

## Problem

How should the canonical entity model be structured to support:
- Multiple domains (aircraft, vessels, vehicles, IoT, drones)
- Zero-copy transport to browsers
- Cache-friendly sequential processing
- GPU-compatible memory layout
- Spatial indexing

## Alternatives

### Option A: Tagged Union (per-domain structs)

Different struct layouts for each entity type. Tagged dispatch.

### Option B: ECS (Entity Component System)

Separate arrays for position, velocity, metadata. Archetype-based storage.

### Option C: Fixed Flat Struct (universal fields)

Single 64-byte struct with fields common to all spatial entities.
Domain-specific metadata stored separately.

## Tradeoffs

| Criterion            | Tagged Union  | ECS          | Fixed Flat     |
|----------------------|---------------|--------------|----------------|
| Domain independence  | ✗ (per-type)  | ✓            | ✓✓             |
| SharedArrayBuffer    | ✗             | △ (multiple) | ✓✓             |
| Cache locality       | △             | ✓✓           | ✓✓             |
| GPU compatibility    | ✗             | ✓            | ✓✓             |
| Simplicity           | ★★☆☆☆        | ★★★☆☆       | ★★★★★          |
| Extensibility        | ★★★★☆        | ★★★★★       | ★★★☆☆          |
| Network efficiency   | ★★★☆☆        | ★★★★☆       | ★★★★★          |
| Sequential scan perf | ★★☆☆☆        | ★★★★★       | ★★★★★          |

## Benchmark Evidence

Memory access pattern analysis:

- **Fixed flat struct**: Sequential scan of N entities = N × 64 bytes contiguous.
  Perfect L1 cache prefetch pattern. Hardware prefetcher engaged.
- **Tagged union**: Variable sizes break prefetch. Branch prediction overhead for type dispatch.
- **ECS**: Excellent for selective component access but requires multiple buffer
  bindings for GPU and multiple SharedArrayBuffer views in browser.

For the primary use case (render all entities every frame), flat struct wins because
ALL fields are needed simultaneously.

## Decision

**Fixed Flat Struct (64-byte universal EntityState)**

Fields chosen to represent the common spatial attributes across all domains:
- Position (lat, lon, alt) — universal for any spatial entity
- Velocity (speed, heading, vrate) — universal for moving entities
- Identity (entity_id, entity_type) — universal
- Temporal (timestamp, sequence) — universal for streaming
- Spatial (grid_cell) — pre-computed for interest management
- Status (flags) — validity bits, quality indicator

Domain-specific metadata (callsign, MMSI, sensor readings, etc.) is handled via:
- Separate metadata messages (lower frequency, not every tick)
- Lookup tables indexed by entity_id
- Not part of the hot path

## Future Revisions

- If 64 bytes proves insufficient for core spatial data, expand to 128 bytes.
- If ECS proves beneficial for selective server-side queries, implement ECS storage
  internally while keeping the wire format as fixed flat struct.
- If domain-specific fields need high-frequency updates, add domain sidecar frames.
