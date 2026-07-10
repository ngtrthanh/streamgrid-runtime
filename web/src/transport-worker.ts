/**
 * StreamGrid Transport Worker
 * 
 * Receives binary entity frames from the edge server via WebSocket (or WebTransport)
 * and writes them directly into a SharedArrayBuffer for zero-copy GPU rendering.
 * 
 * This worker runs in a dedicated thread so transport I/O doesn't block rendering.
 * 
 * Message Protocol (main thread <-> worker):
 *   Main -> Worker: { type: 'init', sab: SharedArrayBuffer, config: {...} }
 *   Main -> Worker: { type: 'connect', url: string, protocol: 'ws' | 'wt' }
 *   Main -> Worker: { type: 'disconnect' }
 *   Worker -> Main: { type: 'connected', protocol: string }
 *   Worker -> Main: { type: 'frame', entityCount: number, timestamp: number }
 *   Worker -> Main: { type: 'stats', fps: number, bytesPerSec: number, latencyMs: number }
 *   Worker -> Main: { type: 'error', message: string }
 *   Worker -> Main: { type: 'disconnected' }
 */

// Entity state layout (64 bytes per entity)
const ENTITY_SIZE = 64;
const FRAME_HEADER_SIZE = 16;
const FRAME_MAGIC = 0x53475246; // "SGRF"

// SharedArrayBuffer layout:
// [0..3]   uint32: frame sequence number (written last as release fence)
// [4..5]   uint16: entity count
// [6..7]   uint16: reserved
// [8..15]  uint64: frame timestamp
// [16..]   EntityState[N] (64 bytes each)
const SAB_HEADER_SIZE = 16;

interface WorkerConfig {
  maxEntities: number;
  statsIntervalMs: number;
}

let sharedBuffer: SharedArrayBuffer | null = null;
let sharedView: DataView | null = null;
let sharedUint32: Uint32Array | null = null;
let config: WorkerConfig = { maxEntities: 100000, statsIntervalMs: 1000 };
let ws: WebSocket | null = null;
let frameCount = 0;
let bytesReceived = 0;
let lastStatsTime = 0;
let lastFrameTime = 0;

// Handle messages from main thread
self.onmessage = (event: MessageEvent) => {
  const msg = event.data;

  switch (msg.type) {
    case 'init':
      initSharedBuffer(msg.sab, msg.config);
      break;
    case 'connect':
      connect(msg.url, msg.protocol || 'ws');
      break;
    case 'disconnect':
      disconnect();
      break;
  }
};

function initSharedBuffer(sab: SharedArrayBuffer, cfg?: Partial<WorkerConfig>) {
  sharedBuffer = sab;
  sharedView = new DataView(sab);
  sharedUint32 = new Uint32Array(sab);
  if (cfg) {
    config = { ...config, ...cfg };
  }
  // Zero out the header
  sharedView.setUint32(0, 0, true); // sequence = 0
  sharedView.setUint16(4, 0, true); // entityCount = 0
}

function connect(url: string, protocol: 'ws' | 'wt') {
  if (protocol === 'ws') {
    connectWebSocket(url);
  } else {
    connectWebTransport(url);
  }
}

function connectWebSocket(url: string) {
  try {
    ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      postMessage({ type: 'connected', protocol: 'websocket' });
      lastStatsTime = performance.now();
    };

    ws.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        processFrame(new Uint8Array(event.data));
      }
    };

    ws.onerror = (event) => {
      postMessage({ type: 'error', message: 'WebSocket error' });
    };

    ws.onclose = () => {
      postMessage({ type: 'disconnected' });
      ws = null;
    };
  } catch (err) {
    postMessage({ type: 'error', message: `Connection failed: ${err}` });
  }
}

async function connectWebTransport(url: string) {
  try {
    // @ts-ignore - WebTransport API may not be in all type definitions
    const transport = new WebTransport(url);
    await transport.ready;
    postMessage({ type: 'connected', protocol: 'webtransport' });
    lastStatsTime = performance.now();

    // Read from incoming unidirectional streams
    const reader = transport.incomingUnidirectionalStreams.getReader();
    const { value: stream } = await reader.read();
    if (!stream) return;

    const streamReader = stream.getReader();
    let buffer = new Uint8Array(0);

    while (true) {
      const { value, done } = await streamReader.read();
      if (done) break;

      // Append to buffer
      const newBuffer = new Uint8Array(buffer.length + value.length);
      newBuffer.set(buffer);
      newBuffer.set(value, buffer.length);
      buffer = newBuffer;

      // Process complete frames (length-prefixed: 4-byte LE length + frame data)
      while (buffer.length >= 4) {
        const frameLen = buffer[0] | (buffer[1] << 8) | (buffer[2] << 16) | (buffer[3] << 24);
        if (buffer.length < 4 + frameLen) break;

        const frameData = buffer.slice(4, 4 + frameLen);
        processFrame(frameData);
        buffer = buffer.slice(4 + frameLen);
      }
    }

    postMessage({ type: 'disconnected' });
  } catch (err) {
    postMessage({ type: 'error', message: `WebTransport failed: ${err}` });
  }
}

function processFrame(data: Uint8Array) {
  if (!sharedView || !sharedBuffer) return;
  if (data.length < FRAME_HEADER_SIZE) return;

  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);

  // Validate magic
  const magic = view.getUint32(0, true);
  if (magic !== FRAME_MAGIC) return;

  const entityCount = view.getUint16(6, true);
  const timestamp = Number(view.getBigUint64(8, true));

  // Validate frame size
  const expectedSize = FRAME_HEADER_SIZE + entityCount * ENTITY_SIZE;
  if (data.length < expectedSize) return;

  // Clamp entity count to max
  const writeCount = Math.min(entityCount, config.maxEntities);

  // Write entity data directly into SharedArrayBuffer (after SAB header)
  const destArray = new Uint8Array(sharedBuffer, SAB_HEADER_SIZE);
  destArray.set(data.subarray(FRAME_HEADER_SIZE, FRAME_HEADER_SIZE + writeCount * ENTITY_SIZE));

  // Write header (entity count and timestamp)
  sharedView.setUint16(4, writeCount, true);
  // Write timestamp as two uint32s
  sharedView.setUint32(8, timestamp & 0xFFFFFFFF, true);
  sharedView.setUint32(12, (timestamp / 0x100000000) >>> 0, true);

  // Write sequence LAST as release fence (Atomics.store ensures ordering)
  frameCount++;
  Atomics.store(sharedUint32!, 0, frameCount);

  // Update stats
  bytesReceived += data.length;
  lastFrameTime = performance.now();

  // Notify main thread
  postMessage({ type: 'frame', entityCount: writeCount, timestamp });

  // Periodic stats
  const now = performance.now();
  if (now - lastStatsTime >= config.statsIntervalMs) {
    const elapsed = (now - lastStatsTime) / 1000;
    const fps = frameCount / ((now - (lastStatsTime - config.statsIntervalMs)) / 1000);
    postMessage({
      type: 'stats',
      fps: Math.round(fps),
      bytesPerSec: Math.round(bytesReceived / elapsed),
      latencyMs: 0, // TODO: measure server-to-render latency
    });
    bytesReceived = 0;
    lastStatsTime = now;
  }
}

function disconnect() {
  if (ws) {
    ws.close();
    ws = null;
  }
  postMessage({ type: 'disconnected' });
}

export {}; // Make this a module
