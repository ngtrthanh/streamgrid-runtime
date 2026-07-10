# ADR-002: Binary Transport Format

**Status:** Accepted
**Date:** 2026-07-09
**Authors:** StreamGrid Team

## Problem

What binary format should be used to transport entity state from server to browser?
The format must support zero-copy decoding in SharedArrayBuffer and minimize CPU overhead.

## Alternatives

### Option A: Protocol Buffers (protobuf)

Industry-standard schema-driven encoding. Variable-length integers, schema evolution.

### Option B: FlatBuffers

Zero-copy access without deserialization. Schema-driven, accessor-based API.

### Option C: Custom Fixed-Size Binary

Fixed 64-byte records. Direct memcpy into SharedArrayBuffer. No schema library needed.

### Option D: Cap'n Proto

Zero-copy with pointer-based traversal. Schema-driven.

### Option E: MessagePack

Compact binary JSON-like format. Self-describing.

## Tradeoffs

| Criterion              | Protobuf | FlatBuffers | Custom Fixed | Cap'n Proto | MsgPack |
|------------------------|----------|-------------|--------------|-------------|---------|
| Zero-copy decode       | ✗        | ✓           | ✓✓           | ✓           | ✗       |
| SharedArrayBuffer safe | ✗        | △           | ✓✓           | △           | ✗       |
| Decode overhead        | High     | Low         | Zero         | Low         | High    |
| Fixed record size      | ✗        | ✗           | ✓            | ✗           | ✗       |
| Cache friendly         | ✗        | △           | ✓✓           | △           | ✗       |
| Schema evolution       | ✓✓       | ✓           | △            | ✓           | ✓       |
| Library deps           | Heavy    | Medium      | None         | Heavy       | Medium  |
| GPU buffer compatible  | ✗        | ✗           | ✓✓           | ✗           | ✗       |
| Simplicity             | ★★★☆☆   | ★★★☆☆      | ★★★★★        | ★★☆☆☆      | ★★★★☆  |

## Benchmark Evidence

Theoretical analysis (benchmarks to follow in Phase 1C):

- **Custom fixed 64-byte**: Decode = memcpy. Cost: ~0ns per entity beyond memory copy.
  For 10K entities: 640KB memcpy → ~50μs on modern hardware.
- **Protobuf**: Requires varint decoding, field tag parsing, allocation.
  Estimated 10-50x slower than memcpy for structured data.
- **FlatBuffers**: Near-zero decode but accessor overhead and vtable lookups.
  Not directly mappable to SharedArrayBuffer DataView pattern.

Phase 1C will produce actual benchmarks comparing custom binary vs JSON baseline.

## Decision

**Custom Fixed-Size Binary (64-byte EntityState records)**

Rationale:
1. The primary use case is bulk entity state fanout to browsers at high frequency.
2. SharedArrayBuffer requires fixed-size records at known offsets for DataView access.
3. Zero decode overhead means the transport→GPU path has no CPU work beyond the memcpy.
4. The record layout matches cache lines for sequential scan performance.
5. No external library dependency — protocol is simple enough to implement directly.
6. GPU compute shaders can read the same memory layout via storage buffers.

Schema evolution is handled via:
- Version field in frame header
- Reserved bytes in entity record (8 bytes available)
- New message types for backward-compatible extensions

## Future Revisions

- If entity state grows beyond 64 bytes, we may move to 128-byte records (2 cache lines).
- If variable-length metadata is needed, a separate sidecar message type will be added.
- If benchmarks show FlatBuffers is competitive, the additional schema evolution support may justify switching.
