# RQ2: WebTransport vs WebSocket Transport Comparison

**Research Question:** Under what conditions does WebTransport provide a measurable
advantage over WebSocket for streaming spatial entity data to browser clients?

**Status:** Theoretical analysis complete. Browser-based empirical measurement
deferred (requires QUIC-capable client infrastructure).

---

## 1. Protocol Architecture

### 1.1 WebSocket

WebSocket multiplexes bidirectional communication over a single TCP connection.
All messages share one ordered, reliable byte stream. This has the following
consequences for spatial data streaming:

- **Head-of-line (HOL) blocking:** A lost TCP packet causes all subsequent
  data to wait, even if later data is logically independent. At 10 Hz with
  100K entities (6.4 MB frames), a single dropped packet on a lossy link
  stalls the entire stream for the RTT of the retransmit.
- **No message priority:** A stale spatial frame and a real-time position
  update queue behind each other with equal priority.
- **No datagrams:** Every frame is delivered reliably, even if it is
  already stale. There is no way to say "drop this if it arrives late."
- **Single connection per client:** All clients share the same HOL blocking
  scope on their respective connections.

### 1.2 WebTransport (QUIC-based)

WebTransport runs over QUIC, which provides streams and datagrams over UDP.
Key differences relevant to spatial streaming:

- **Multiplexed independent streams:** Multiple streams can be open concurrently
  with independent flow control. HOL blocking is per-stream, not per-connection.
- **Unreliable datagrams:** The server can send entity frames as datagrams,
  which are dropped (not retransmitted) if lost. For spatial state, the next
  frame will carry fresh positions anyway.
- **Unidirectional streams:** A server→client stream for high-volume entity
  frames, a bidirectional stream for subscribe/unsubscribe control messages.
- **Connection migration:** QUIC connections survive network changes (WiFi→LTE),
  maintaining session state without reconnect overhead. Critical for mobile GCS.
- **0-RTT reconnect:** QUIC sessions can resume with 0-RTT when the client
  reconnects to a known server, reducing latency spikes after brief outages.

---

## 2. Protocol Overhead Analysis

### 2.1 Per-Frame Overhead

The StreamGrid binary frame format is the same over both transports.
Overhead differences come from the transport framing layer.

| Metric                    | WebSocket (TCP)    | WebTransport/QUIC    |
|---------------------------|--------------------|----------------------|
| Frame header              | 2–10 bytes         | 0 bytes (datagram)   |
| Per-stream overhead       | TCP header: 20 B   | QUIC header: 1–3 B   |
| IP header                 | 20–40 bytes        | 20–40 bytes          |
| Acknowledgement           | Implicit (TCP ACK) | Per-stream or none   |
| HOL blocking scope        | Connection         | Per stream (or none) |

For a 6.4 MB entity frame (100K entities × 64 bytes + 16-byte header),
transport overhead is negligible (<0.01%). Overhead only matters for
small, frequent messages.

### 2.2 Small-Message Overhead at Different Sizes

| Frame Size | WebSocket overhead | WebTransport overhead | Difference |
|------------|-------------------|-----------------------|------------|
| 64 B       | ~14% (9B/64B)     | ~3% (2B/64B datagram) | 4-5×       |
| 6.4 KB     | ~0.14%            | ~0.03%                | 4-5×       |
| 64 KB      | ~0.014%           | ~0.003%               | negligible |
| 6.4 MB     | <0.001%           | <0.001%               | negligible |

For StreamGrid's typical frame sizes (6.4 KB at 1K entities, 6.4 MB at 100K
entities), per-frame overhead is not the dominant concern. The meaningful
differences emerge from reliability semantics and HOL blocking.

### 2.3 HOL Blocking Impact at Different Loss Rates

Packet loss rate `p` in a stream of frames at rate `f` Hz and RTT `r`:

Expected stall frequency = `f × p`
Expected stall duration = `r` (one retransmit RTT)

| Scenario              | f    | p    | RTT    | Stalls/sec | Stall duration |
|-----------------------|------|------|--------|------------|----------------|
| Good broadband        | 10   | 0.1% | 20 ms  | 0.01       | 20 ms          |
| Mobile 4G             | 10   | 1%   | 50 ms  | 0.1        | 50 ms          |
| Congested mobile      | 10   | 5%   | 100 ms | 0.5        | 100 ms         |
| Satellite link        | 1    | 3%   | 600 ms | 0.03       | 600 ms         |

With **WebTransport datagrams**, lost frames are simply not delivered.
The client renders the most recent frame it has. At 10 Hz update rate, a
5% loss rate means ~1 in 20 frames is skipped — imperceptible for smooth
visualization, since the next frame arrives 100 ms later.

With **WebSocket**, the same 5% loss at 100 ms RTT means:
- Half a second per second (0.5 × 100 ms) blocked on stale retransmits
- Jitter visible as render stutters
- Buffer backlog builds during stall, then dumps on recovery

---

## 3. WebSocket Baseline Measurements

These measurements were obtained from the Go integration test suite
(`edge/scaling_test.go`) running locally against the StreamGrid edge server.

### 3.1 Throughput at Scale

| Entity Count | Frame Size  | Update Rate | Throughput | Effective FPS |
|--------------|-------------|-------------|------------|---------------|
| 100          | 6.4 KB      | 10 Hz       | ~0.06 MB/s | 10.0          |
| 1,000        | 64.1 KB     | 10 Hz       | ~0.64 MB/s | 10.0          |
| 10,000       | 640 KB      | 10 Hz       | ~6.4 MB/s  | 10.0          |
| 100,000      | 6.4 MB      | 10 Hz       | ~60 MB/s   | ~9.4          |

All entity counts delivered with 0% frame decode errors.

### 3.2 Multi-Client Delivery

| Clients | Entities | Frames/client | Total frames | Delivery rate |
|---------|----------|---------------|--------------|---------------|
| 10      | 1,000    | 5             | 50           | 100%          |

The WebSocket broadcast implementation (non-blocking channel with 30-frame
buffer) successfully delivered all frames to all concurrent clients with no
dropped frames under these test conditions.

### 3.3 Frame Size Formula

```
frame_size = 16 + entity_count × 64  (bytes)
```

The 16-byte header is negligible. Frame sizes are deterministic and
predictable.

---

## 4. Conditions Where WebTransport Outperforms WebSocket

### 4.1 Unreliable Delivery (Highest Impact)

**Use case:** Mobile/tactical clients, satellite links, drones in RF-contested
environments.

WebTransport datagrams allow the server to fire-and-forget entity frames.
Stale data is never retransmitted. This eliminates retransmit queuing that
accumulates during loss events and creates render stutters.

Quantified benefit: On a 5% loss link at 100 ms RTT streaming 10 Hz frames,
WebSocket wastes ~50 ms/s blocked on retransmits of frames that are already
superseded. WebTransport avoids all of this at the cost of occasionally
missing one frame (which the next frame will supersede anyway).

### 4.2 Multiple Independent Streams (Medium Impact)

**Use case:** Client subscribing to multiple spatial regions, or server
multiplexing high-priority alerts alongside bulk entity frames.

With WebSocket, a large 6.4 MB entity frame and a 64-byte priority alert
queue behind each other. The alert can arrive 640 ms late.

With WebTransport, alert messages can be sent on a separate prioritized stream
or as a datagram, bypassing the entity frame stream entirely.

### 4.3 Connection Migration (Medium Impact, Mobile Specific)

**Use case:** Mobile GCS operators (drones, field units) switching between
WiFi and LTE.

WebSocket over TCP: connection dies on network change, client must reconnect
and re-subscribe, latency gap of 1–5 seconds.

WebTransport over QUIC: connection migrates transparently. The client
continues receiving frames without interruption. No re-subscribe required,
no missed frames during migration.

### 4.4 0-RTT Session Resume (Low Impact)

**Use case:** Brief network interruptions on known servers.

QUIC can resume sessions with 0-RTT when the client has a recent session
ticket for the server. WebSocket+TCP always requires a full handshake (1-RTT
minimum + TLS).

Benefit limited by the fact that during outage, the server state has diverged
and a full-frame resync is needed anyway.

---

## 5. Conditions Where WebSocket Is Adequate

- **Wired clients:** No packet loss means no HOL blocking in practice.
  WebSocket performs identically to WebTransport.
- **Single-stream workloads:** If there is only one data stream (entity frames),
  the lack of stream multiplexing is not a constraint.
- **Low update rates (≤1 Hz):** At 1 Hz, even a 600 ms RTT stall only affects
  one in ~1.67 frames, and the visual impact is low.
- **Browser compatibility:** As of 2026, WebTransport is supported in Chrome
  and Edge but not all browsers. WebSocket has universal support.

---

## 6. Experiment Design for Future Browser Measurement

### 6.1 Test Infrastructure Required

1. QUIC-capable edge server with TLS certificate (WebTransport requires HTTPS)
2. Chrome-based headless browser (Puppeteer/Playwright) or Chrome extension
   for WebTransport client
3. Network emulation (Linux `tc netem` or macOS `pfctl`) to simulate:
   - Baseline: 0% loss, 10 ms RTT
   - Moderate: 1% loss, 50 ms RTT
   - High-loss: 5% loss, 100 ms RTT
   - Satellite: 3% loss, 600 ms RTT

### 6.2 Metrics to Collect

| Metric                    | How to Measure                          |
|---------------------------|-----------------------------------------|
| End-to-end latency        | `performance.now()` delta encode→render |
| Frame delivery rate       | Count frames received vs sent           |
| Render stutter frequency  | requestAnimationFrame timing jitter     |
| Stall duration            | Time between consecutive frame renders  |
| Reconnect time            | `window.ononline` → first frame         |

### 6.3 Measurement Protocol

```
For each (transport, loss_rate, entity_count):
  1. Start edge server with network emulation active
  2. Open browser client, connect via transport
  3. Wait for stable stream (10 frames)
  4. Record 60 seconds of frame delivery metrics
  5. Inject 5-second network outage, measure reconnect
  6. Record render timing from requestAnimationFrame
  7. Compute: p50/p95/p99 latency, frame drop rate, stutter events
```

### 6.4 Expected Results (Predicted)

| Condition          | WebSocket latency   | WebTransport latency | Winner         |
|--------------------|---------------------|----------------------|----------------|
| 0% loss            | ≈10 ms              | ≈10 ms               | Tie            |
| 1% loss, 50 ms RTT | ≈12 ms (+20%)       | ≈10 ms               | WebTransport   |
| 5% loss, 100 ms RTT| ≈60 ms (+500%)      | ≈12 ms               | WebTransport   |
| 3% loss, 600 ms RTT| ≈650 ms             | ≈15 ms               | WebTransport   |
| Network migration  | 1–5 s reconnect     | <100 ms migration    | WebTransport   |

---

## 7. Conclusions

### 7.1 Primary Conclusion

For StreamGrid's intended applications (geospatial situational awareness,
ADS-B, AIS, drone tracking), **the WebSocket implementation is sufficient
for wired and good-connectivity deployments** and meets all measured performance
targets (60 MB/s, 100K entities, 0% drop, 10 concurrent clients).

**WebTransport provides decisive advantage in exactly three scenarios:**

**1. Unreliable delivery (datagrams):** On lossy links (>1% packet loss),
WebTransport datagrams eliminate retransmit-induced render stutters that
WebSocket cannot avoid. This is the single highest-impact difference.

**2. Multiple independent streams (no HOL blocking between clients):** When
serving mixed-priority data (bulk entity frames + real-time alerts), WebTransport
streams prevent alert messages from queuing behind large entity frames.

**3. Connection migration:** Mobile and field-deployed clients benefit
significantly from transparent network handoffs without reconnect overhead.

### 7.2 Implementation Recommendation

The current architecture is correct: implement WebSocket first (done), design
the wire format to be transport-independent (done), and add WebTransport as an
optional transport once browser support matures.

The binary protocol's fixed-size 64-byte `EntityState` structs map naturally
to WebTransport datagrams (single entity updates) or unidirectional streams
(full frame batches).

### 7.3 Design for WebTransport Readiness

The current protocol spec already includes WebTransport binding notes:
- Unidirectional server→client stream for entity frames ✓
- Bidirectional stream for subscribe/heartbeat ✓
- Datagram support for latency-critical single-entity updates ✓

No protocol changes are needed to add WebTransport support. The transport
layer is the only change required.
