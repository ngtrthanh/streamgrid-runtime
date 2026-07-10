# Protocol

Binary protocol encoder/decoder for StreamGrid entity state transport.

## Design Goals

- Minimal serialization overhead
- Zero-copy decoding where possible
- Fixed-size entity records for cache-friendly access
- Version-aware for forward/backward compatibility

## Language

Rust (latest stable) — with Go bindings for server use
