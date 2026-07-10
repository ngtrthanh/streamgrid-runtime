# Canonical Entity Model

## Overview

The canonical entity model is the **transport-independent** internal representation
of a spatial entity within StreamGrid. All domain-specific data (ADS-B, AIS, IoT, etc.)
is normalized into this common representation before processing, indexing, or transport.

Transport encoders are **adapters** — they serialize this model into specific wire formats.

## Entity State Record

```
┌─────────────────────────────────────────────────────────────────────┐
│ EntityState (64 bytes, fixed-size, cache-line aligned)              │
├─────────────────────────────────────────────────────────────────────┤
│ Field            │ Type     │ Bytes │ Offset │ Description          │
├──────────────────┼──────────┼───────┼────────┼──────────────────────┤
│ entity_id        │ uint32   │ 4     │ 0      │ Unique entity ID     │
│ flags            │ uint16   │ 2     │ 4      │ Status flags         │
│ entity_type      │ uint8    │ 1     │ 6      │ Entity category      │
│ _padding         │ uint8    │ 1     │ 7      │ Alignment padding    │
│ timestamp_ms     │ uint64   │ 8     │ 8      │ Unix epoch millis    │
│ latitude         │ float64  │ 8     │ 16     │ WGS84 degrees        │
│ longitude        │ float64  │ 8     │ 24     │ WGS84 degrees        │
│ altitude_m       │ float32  │ 4     │ 32     │ Meters above WGS84   │
│ speed_ms         │ float32  │ 4     │ 36     │ Ground speed m/s     │
│ heading_deg      │ float32  │ 4     │ 40     │ True heading degrees │
│ vrate_ms         │ float32  │ 4     │ 44     │ Vertical rate m/s    │
│ sequence         │ uint32   │ 4     │ 48     │ Update sequence num  │
│ grid_cell        │ uint32   │ 4     │ 52     │ Spatial grid cell ID │
│ _reserved        │ [8]byte  │ 8     │ 56     │ Future use           │
└─────────────────────────────────────────────────────────────────────┘
Total: 64 bytes (exactly one cache line on most architectures)
```

## Design Rationale

1. **Fixed 64 bytes** — Fits exactly in one x86 cache line. Array of entities gives
   perfect cache locality for sequential scans.

2. **No pointers/strings** — Flat memory layout, safe for SharedArrayBuffer, memcpy,
   and zero-copy transport.

3. **Native numeric types** — No encoding/decoding needed for in-memory access.
   SharedArrayBuffer views can read fields directly via DataView.

4. **Grid cell pre-computed** — Spatial index lookup accelerated by pre-assigned cell.

5. **Sequence number** — Enables stale-update detection without timestamps comparison.

## Flags Field (uint16)

```
Bit 0:     Active (1 = active, 0 = stale/expired)
Bit 1:     Position valid
Bit 2:     Altitude valid
Bit 3:     Speed valid
Bit 4:     Heading valid
Bit 5:     Vertical rate valid
Bit 6-7:   Quality indicator (0=unknown, 1=low, 2=medium, 3=high)
Bit 8-15:  Reserved
```

## Entity Types (uint8)

```
0x00  Unknown
0x01  Aircraft
0x02  Vessel
0x03  Vehicle
0x04  Person
0x05  Drone/UAV
0x06  Satellite
0x07  Sensor (fixed)
0x08  Robot
0x09  Asset (generic movable)
0x0A-0xFF Reserved
```

## Transport Encodings

The canonical model can be serialized to:

| Format          | Use Case                           |
|-----------------|------------------------------------|
| Raw Binary      | SharedArrayBuffer, direct memcpy   |
| Compact Binary  | WebTransport wire format           |
| Debug JSON      | Debugging, logging                 |
| Replay Format   | Deterministic recording/playback   |

**Application logic MUST NOT depend on transport encoding.**

## Grid Cell Calculation

Default spatial partitioning uses a simple grid:

```
cell_x = floor((longitude + 180) / cell_size_deg)
cell_y = floor((latitude + 90) / cell_size_deg)
grid_cell = cell_y * grid_width + cell_x
```

Default cell size: 1.0 degree (configurable).
Grid width at 1°: 360 cells.

## Frame Format

A frame contains a batch of entity states for a single update tick:

```
┌──────────────────────────────────────────┐
│ Frame Header (16 bytes)                  │
├──────────────────────────────────────────┤
│ magic          │ uint32 │ 0x53475246     │ "SGRF"
│ version        │ uint8  │ 1             │
│ frame_type     │ uint8  │ 0=full, 1=delta│
│ entity_count   │ uint16 │ N             │
│ timestamp_ms   │ uint64 │ frame time    │
├──────────────────────────────────────────┤
│ EntityState[0]  (64 bytes)              │
│ EntityState[1]  (64 bytes)              │
│ ...                                      │
│ EntityState[N-1] (64 bytes)             │
└──────────────────────────────────────────┘
Total frame size: 16 + (N × 64) bytes
```

## Memory Layout in SharedArrayBuffer

```
Offset 0:       Frame header (16 bytes)
Offset 16:      EntityState[0]
Offset 80:      EntityState[1]
...
Offset 16+i*64: EntityState[i]
```

Browser workers write into this buffer. The GPU renderer reads from it.
No copies, no serialization on the render path.

## Versioning

- Version field in frame header enables future evolution
- Entity record size is fixed at 64 bytes for v1
- Reserved bytes allow adding fields without breaking alignment
