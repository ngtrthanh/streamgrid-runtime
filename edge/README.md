# Edge

Edge servers for StreamGrid — WebTransport endpoints that stream telemetry to browsers.

## Responsibilities

- Accept WebTransport connections from browsers
- Apply spatial interest management (send only relevant entities)
- Encode entity state using binary protocol
- Manage client subscriptions and viewport tracking

## Language

Go (latest stable)
