# Web

Browser client for StreamGrid — workers, WebTransport client, SharedArrayBuffer management.

## Architecture

- **Main thread** — UI, renderer coordination
- **Transport worker** — WebTransport connection, receives binary frames
- **Decode worker** — Writes entity state into SharedArrayBuffer
- SharedArrayBuffer is read directly by GPU renderer (zero-copy)

## Language

TypeScript
