# StreamGrid Binary Protocol Specification v1

## Overview

The StreamGrid binary protocol defines how entity state is transmitted between
components. It is designed for:

- Zero-copy path to SharedArrayBuffer
- Minimal encoding/decoding overhead
- Cache-friendly sequential access
- Direct memory-mapping compatibility

## Wire Format

All values are **little-endian** (matches x86, ARM, WebAssembly).

### Message Types

```
0x01  EntityFrame     — Batch of entity states
0x02  Subscribe       — Client subscribes to spatial region
0x03  Unsubscribe     — Client unsubscribes from region
0x04  Heartbeat       — Keep-alive
0x05  ServerInfo      — Server capabilities and config
```

### EntityFrame Message

```
┌─────────────────────────────────────────────────────────┐
│ Message Header (4 bytes)                                │
├─────────────────────────────────────────────────────────┤
│ msg_type     │ uint8   │ 0x01                          │
│ msg_flags    │ uint8   │ compression, priority         │
│ msg_length   │ uint16  │ payload length (excl header)  │
├─────────────────────────────────────────────────────────┤
│ Frame Header (16 bytes)                                 │
├─────────────────────────────────────────────────────────┤
│ magic        │ uint32  │ 0x53475246 ("SGRF")           │
│ version      │ uint8   │ 1                             │
│ frame_type   │ uint8   │ 0=full, 1=delta               │
│ entity_count │ uint16  │ N                             │
│ timestamp_ms │ uint64  │ frame timestamp               │
├─────────────────────────────────────────────────────────┤
│ Entity Records (N × 64 bytes)                           │
│ ... (see entity-model.md)                              │
└─────────────────────────────────────────────────────────┘
```

### Subscribe Message

```
┌─────────────────────────────────────────────────────────┐
│ msg_type     │ uint8   │ 0x02                          │
│ msg_flags    │ uint8   │ 0                             │
│ msg_length   │ uint16  │ 32                            │
├─────────────────────────────────────────────────────────┤
│ min_lat      │ float64 │ SW corner latitude            │
│ min_lon      │ float64 │ SW corner longitude           │
│ max_lat      │ float64 │ NE corner latitude            │
│ max_lon      │ float64 │ NE corner longitude           │
└─────────────────────────────────────────────────────────┘
```

### Heartbeat Message

```
┌─────────────────────────────────────────────────────────┐
│ msg_type     │ uint8   │ 0x04                          │
│ msg_flags    │ uint8   │ 0                             │
│ msg_length   │ uint16  │ 8                             │
├─────────────────────────────────────────────────────────┤
│ timestamp_ms │ uint64  │ sender timestamp              │
└─────────────────────────────────────────────────────────┘
```

### ServerInfo Message

```
┌─────────────────────────────────────────────────────────┐
│ msg_type     │ uint8   │ 0x05                          │
│ msg_flags    │ uint8   │ 0                             │
│ msg_length   │ uint16  │ 16                            │
├─────────────────────────────────────────────────────────┤
│ max_entities │ uint32  │ max entities per frame        │
│ tick_rate_hz │ uint16  │ server update rate            │
│ protocol_ver │ uint8   │ protocol version              │
│ _reserved    │ uint8   │                               │
│ grid_cell_sz │ float32 │ spatial grid cell degrees     │
│ _reserved2   │ uint32  │                               │
└─────────────────────────────────────────────────────────┘
```

## Design Decisions

1. **Little-endian** — Native on x86/ARM/WASM. No byte-swapping needed.
2. **Fixed entity size** — Enables direct array indexing. `entity[i]` = `base + i*64`.
3. **No length-prefixed strings** — All identifiers are numeric.
4. **Message header first** — Enables framing without buffering entire message.
5. **Delta frames** — Only send changed entities. Full frames for initial sync.

## Bandwidth Estimates

| Entities | Update Rate | Frame Size  | Bandwidth     |
|----------|-------------|-------------|---------------|
| 100      | 10 Hz       | 6.4 KB      | 64 KB/s       |
| 1,000    | 10 Hz       | 64 KB       | 640 KB/s      |
| 10,000   | 10 Hz       | 640 KB      | 6.4 MB/s      |
| 100,000  | 10 Hz       | 6.4 MB      | 64 MB/s       |
| 100,000  | 1 Hz        | 6.4 MB      | 6.4 MB/s      |

At 100K entities @ 1 Hz with delta frames (typical 10% change rate):
~640 KB/s — very manageable.

## Compression

Optional zstd compression for delta frames (indicated in msg_flags bit 0).
Full frames are uncompressed for zero-copy SharedArrayBuffer write.

## Transport Binding

### WebTransport

- Unidirectional server→client stream for entity frames
- Bidirectional stream for subscribe/heartbeat
- Datagrams for latency-critical single-entity updates

### WebSocket (fallback)

- Binary frames with same message format
- No datagram support — all messages on single ordered stream
