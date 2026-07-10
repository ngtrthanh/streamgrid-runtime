/**
 * StreamGrid Client
 * 
 * Main-thread API for connecting to a StreamGrid edge server.
 * Creates a SharedArrayBuffer and spawns a transport worker.
 * The renderer reads entity data directly from the SharedArrayBuffer.
 * 
 * Usage:
 *   const client = new StreamGridClient({ maxEntities: 10000 });
 *   await client.connect('ws://localhost:8080/ws');
 *   // In render loop:
 *   const { entityCount, sequence } = client.getFrameInfo();
 *   const positions = client.getEntityBuffer(); // SharedArrayBuffer
 */

export const ENTITY_SIZE = 64;
export const FRAME_HEADER_SIZE = 16;
export const SAB_HEADER_SIZE = 16;

/** Byte offsets within each 64-byte EntityState record */
export const EntityOffsets = {
  entityId: 0,      // uint32
  flags: 4,         // uint16
  entityType: 6,    // uint8
  timestampMs: 8,   // uint64
  latitude: 16,     // float64
  longitude: 24,    // float64
  altitudeM: 32,    // float32
  speedMs: 36,      // float32
  headingDeg: 40,   // float32
  vrateMs: 44,      // float32
  sequence: 48,     // uint32
  gridCell: 52,     // uint32
} as const;

export interface ClientConfig {
  maxEntities: number;
  statsIntervalMs: number;
  workerUrl?: string;
}

export interface FrameInfo {
  sequence: number;
  entityCount: number;
  timestamp: number;
}

export interface ClientStats {
  fps: number;
  bytesPerSec: number;
  latencyMs: number;
  entityCount: number;
  connected: boolean;
}

export type ClientEventHandler = (event: ClientEvent) => void;

export type ClientEvent =
  | { type: 'connected'; protocol: string }
  | { type: 'disconnected' }
  | { type: 'frame'; entityCount: number; timestamp: number }
  | { type: 'stats'; fps: number; bytesPerSec: number; latencyMs: number }
  | { type: 'error'; message: string };

export class StreamGridClient {
  private config: ClientConfig;
  private sharedBuffer: SharedArrayBuffer;
  private sharedView: DataView;
  private sharedUint32: Uint32Array;
  private worker: Worker | null = null;
  private connected = false;
  private lastStats: ClientStats = {
    fps: 0, bytesPerSec: 0, latencyMs: 0, entityCount: 0, connected: false
  };
  private eventHandlers: ClientEventHandler[] = [];

  constructor(config: Partial<ClientConfig> = {}) {
    this.config = {
      maxEntities: config.maxEntities ?? 100000,
      statsIntervalMs: config.statsIntervalMs ?? 1000,
      workerUrl: config.workerUrl,
    };

    // Allocate SharedArrayBuffer: header + maxEntities * 64 bytes
    const bufferSize = SAB_HEADER_SIZE + this.config.maxEntities * ENTITY_SIZE;
    this.sharedBuffer = new SharedArrayBuffer(bufferSize);
    this.sharedView = new DataView(this.sharedBuffer);
    this.sharedUint32 = new Uint32Array(this.sharedBuffer);
  }

  /** Get the SharedArrayBuffer containing entity data. Pass to renderer. */
  getSharedBuffer(): SharedArrayBuffer {
    return this.sharedBuffer;
  }

  /** Read current frame info from the SharedArrayBuffer header. */
  getFrameInfo(): FrameInfo {
    const sequence = Atomics.load(this.sharedUint32, 0);
    const entityCount = this.sharedView.getUint16(4, true);
    const timestampLow = this.sharedView.getUint32(8, true);
    const timestampHigh = this.sharedView.getUint32(12, true);
    const timestamp = timestampLow + timestampHigh * 0x100000000;
    return { sequence, entityCount, timestamp };
  }

  /** Get the latest stats. */
  getStats(): ClientStats {
    return { ...this.lastStats, connected: this.connected };
  }

  /** Register an event handler. */
  on(handler: ClientEventHandler): void {
    this.eventHandlers.push(handler);
  }

  /** Connect to edge server. */
  async connect(url: string, protocol: 'ws' | 'wt' = 'ws'): Promise<void> {
    // Create worker
    const workerUrl = this.config.workerUrl || new URL('./transport-worker.ts', import.meta.url).href;
    this.worker = new Worker(workerUrl, { type: 'module' });

    this.worker.onmessage = (event: MessageEvent) => {
      this.handleWorkerMessage(event.data);
    };

    // Initialize worker with SharedArrayBuffer
    this.worker.postMessage({
      type: 'init',
      sab: this.sharedBuffer,
      config: {
        maxEntities: this.config.maxEntities,
        statsIntervalMs: this.config.statsIntervalMs,
      },
    });

    // Connect
    this.worker.postMessage({ type: 'connect', url, protocol });
  }

  /** Disconnect from server. */
  disconnect(): void {
    if (this.worker) {
      this.worker.postMessage({ type: 'disconnect' });
    }
  }

  /** Destroy the client, terminating the worker. */
  destroy(): void {
    if (this.worker) {
      this.worker.terminate();
      this.worker = null;
    }
    this.connected = false;
  }

  private handleWorkerMessage(msg: any): void {
    switch (msg.type) {
      case 'connected':
        this.connected = true;
        this.emit(msg);
        break;
      case 'disconnected':
        this.connected = false;
        this.emit(msg);
        break;
      case 'frame':
        this.lastStats.entityCount = msg.entityCount;
        this.emit(msg);
        break;
      case 'stats':
        this.lastStats.fps = msg.fps;
        this.lastStats.bytesPerSec = msg.bytesPerSec;
        this.lastStats.latencyMs = msg.latencyMs;
        this.emit(msg);
        break;
      case 'error':
        this.emit(msg);
        break;
    }
  }

  private emit(event: ClientEvent): void {
    for (const handler of this.eventHandlers) {
      handler(event);
    }
  }
}

/**
 * Utility: Read entity positions from SharedArrayBuffer as Float64Array pairs.
 * Returns [lat0, lon0, lat1, lon1, ...] for the given entity range.
 */
export function readEntityPositions(
  sab: SharedArrayBuffer,
  startIndex: number,
  count: number
): Float64Array {
  const positions = new Float64Array(count * 2);
  const view = new DataView(sab);

  for (let i = 0; i < count; i++) {
    const offset = SAB_HEADER_SIZE + (startIndex + i) * ENTITY_SIZE;
    positions[i * 2] = view.getFloat64(offset + EntityOffsets.latitude, true);
    positions[i * 2 + 1] = view.getFloat64(offset + EntityOffsets.longitude, true);
  }

  return positions;
}

/**
 * Utility: Read a single entity's full state from SharedArrayBuffer.
 */
export function readEntityState(sab: SharedArrayBuffer, index: number) {
  const view = new DataView(sab);
  const offset = SAB_HEADER_SIZE + index * ENTITY_SIZE;

  return {
    entityId: view.getUint32(offset + EntityOffsets.entityId, true),
    flags: view.getUint16(offset + EntityOffsets.flags, true),
    entityType: view.getUint8(offset + EntityOffsets.entityType),
    latitude: view.getFloat64(offset + EntityOffsets.latitude, true),
    longitude: view.getFloat64(offset + EntityOffsets.longitude, true),
    altitudeM: view.getFloat32(offset + EntityOffsets.altitudeM, true),
    speedMs: view.getFloat32(offset + EntityOffsets.speedMs, true),
    headingDeg: view.getFloat32(offset + EntityOffsets.headingDeg, true),
    vrateMs: view.getFloat32(offset + EntityOffsets.vrateMs, true),
    sequence: view.getUint32(offset + EntityOffsets.sequence, true),
    gridCell: view.getUint32(offset + EntityOffsets.gridCell, true),
  };
}
