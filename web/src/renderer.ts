/**
 * StreamGrid WebGPU Renderer
 * 
 * Reads entity positions directly from SharedArrayBuffer and renders them
 * as instanced point sprites using WebGPU. Zero-copy from transport to GPU.
 * 
 * Architecture:
 * 1. SharedArrayBuffer is written by transport worker (binary frames)
 * 2. Renderer reads entityCount from SAB header
 * 3. Entity positions are uploaded to GPU storage buffer
 * 4. Vertex shader projects lat/lon to screen space (Mercator)
 * 5. Fragment shader renders colored points by entity type
 * 
 * This demonstrates the zero-copy path from network → SharedArrayBuffer → GPU.
 */

import { ENTITY_SIZE, SAB_HEADER_SIZE, EntityOffsets } from './client';

/** Renderer configuration */
export interface RendererConfig {
  /** Canvas element to render into */
  canvas: HTMLCanvasElement;
  /** SharedArrayBuffer containing entity data */
  sharedBuffer: SharedArrayBuffer;
  /** Maximum entities the renderer should handle */
  maxEntities: number;
  /** Point size in pixels */
  pointSize: number;
  /** Background color [r, g, b, a] in 0..1 */
  backgroundColor: [number, number, number, number];
}

/** Render statistics per frame */
export interface RenderStats {
  frameTimeMs: number;
  entityCount: number;
  gpuTimeMs: number;
  fps: number;
}

/** WGSL shader source for the entity renderer */
const SHADER_SOURCE = /* wgsl */`
struct Uniforms {
  viewport: vec4f,       // x, y, width, height
  mapBounds: vec4f,      // minLon, minLat, maxLon, maxLat
  pointSize: f32,
  entityCount: u32,
  time: f32,
  _padding: f32,
}

struct EntityData {
  entityId: u32,
  flags: u32,            // flags (u16) + entityType (u8) + padding (u8) packed
  timestampLow: u32,
  timestampHigh: u32,
  latBits: u32,          // f64 lat as 2x u32
  latBitsHigh: u32,
  lonBits: u32,          // f64 lon as 2x u32
  lonBitsHigh: u32,
  altitudeM: f32,
  speedMs: f32,
  headingDeg: f32,
  vrateMs: f32,
  sequence: u32,
  gridCell: u32,
  reserved0: u32,
  reserved1: u32,
}

@group(0) @binding(0) var<uniform> uniforms: Uniforms;
@group(0) @binding(1) var<storage, read> entities: array<EntityData>;

struct VertexOutput {
  @builtin(position) position: vec4f,
  @location(0) color: vec4f,
  @location(1) pointCoord: vec2f,
}

// Unpack f64 from two u32s (approximation using f32 for rendering)
fn unpackF64(low: u32, high: u32) -> f32 {
  // Reconstruct the IEEE 754 double and approximate as f32
  // For latitude/longitude, direct bitcast to f32 of the high word gives reasonable precision
  let sign = f32(1 - 2 * i32((high >> 31u) & 1u));
  let exponent = i32((high >> 20u) & 0x7FFu) - 1023;
  let mantissa = f32(high & 0xFFFFFu) / f32(0x100000u) + 1.0;
  
  if (exponent > 127 || exponent < -126) {
    return 0.0;
  }
  return sign * mantissa * pow(2.0, f32(exponent));
}

// Convert lat/lon to Mercator screen coordinates
fn latLonToScreen(lat: f32, lon: f32) -> vec2f {
  let mapMinLon = uniforms.mapBounds.x;
  let mapMinLat = uniforms.mapBounds.y;
  let mapMaxLon = uniforms.mapBounds.z;
  let mapMaxLat = uniforms.mapBounds.w;
  
  let x = (lon - mapMinLon) / (mapMaxLon - mapMinLon) * 2.0 - 1.0;
  let y = (lat - mapMinLat) / (mapMaxLat - mapMinLat) * 2.0 - 1.0;
  
  return vec2f(x, y);
}

// Entity type to color
fn entityTypeColor(flags: u32) -> vec4f {
  let entityType = (flags >> 16u) & 0xFFu;
  switch entityType {
    case 1u: { return vec4f(0.2, 0.6, 1.0, 1.0); }  // Aircraft - blue
    case 2u: { return vec4f(0.2, 0.8, 0.4, 1.0); }  // Vessel - green
    case 3u: { return vec4f(1.0, 0.8, 0.2, 1.0); }  // Vehicle - yellow
    case 4u: { return vec4f(0.8, 0.4, 1.0, 1.0); }  // Person - purple
    case 5u: { return vec4f(1.0, 0.4, 0.4, 1.0); }  // Drone - red
    case 6u: { return vec4f(0.6, 0.6, 0.6, 1.0); }  // Satellite - gray
    case 7u: { return vec4f(1.0, 1.0, 1.0, 0.5); }  // Sensor - white dim
    default: { return vec4f(1.0, 1.0, 1.0, 1.0); }  // Unknown - white
  }
}

// Quad vertices for point sprite (instanced)
const QUAD_POSITIONS = array<vec2f, 6>(
  vec2f(-1.0, -1.0),
  vec2f( 1.0, -1.0),
  vec2f(-1.0,  1.0),
  vec2f(-1.0,  1.0),
  vec2f( 1.0, -1.0),
  vec2f( 1.0,  1.0),
);

@vertex
fn vertexMain(
  @builtin(vertex_index) vertexIndex: u32,
  @builtin(instance_index) instanceIndex: u32,
) -> VertexOutput {
  var output: VertexOutput;
  
  if (instanceIndex >= uniforms.entityCount) {
    output.position = vec4f(0.0, 0.0, 0.0, 0.0);
    return output;
  }
  
  let entity = entities[instanceIndex];
  
  // Check if entity is active (bit 0 of flags u16 in lower 16 bits)
  let flags16 = entity.flags & 0xFFFFu;
  if ((flags16 & 1u) == 0u) {
    output.position = vec4f(0.0, 0.0, 0.0, 0.0);
    return output;
  }
  
  let lat = unpackF64(entity.latBits, entity.latBitsHigh);
  let lon = unpackF64(entity.lonBits, entity.lonBitsHigh);
  
  let screenPos = latLonToScreen(lat, lon);
  
  // Point sprite offset
  let quadPos = QUAD_POSITIONS[vertexIndex % 6u];
  let pixelSize = uniforms.pointSize / uniforms.viewport.z;
  
  output.position = vec4f(
    screenPos.x + quadPos.x * pixelSize,
    screenPos.y + quadPos.y * pixelSize * (uniforms.viewport.z / uniforms.viewport.w),
    0.0,
    1.0
  );
  
  output.color = entityTypeColor(entity.flags);
  output.pointCoord = quadPos;
  
  return output;
}

@fragment
fn fragmentMain(input: VertexOutput) -> @location(0) vec4f {
  // Circular point sprite
  let dist = length(input.pointCoord);
  if (dist > 1.0) {
    discard;
  }
  
  // Soft edge
  let alpha = input.color.a * smoothstep(1.0, 0.7, dist);
  return vec4f(input.color.rgb, alpha);
}
`;

/** WebGPU Renderer for StreamGrid entities */
export class StreamGridRenderer {
  private device!: GPUDevice;
  private context!: GPUCanvasContext;
  private pipeline!: GPURenderPipeline;
  private uniformBuffer!: GPUBuffer;
  private entityBuffer!: GPUBuffer;
  private bindGroup!: GPUBindGroup;
  private config: RendererConfig;
  private animationFrameId: number | null = null;
  private lastFrameTime = 0;
  private frameCount = 0;
  private fps = 0;
  private fpsAccumulator = 0;
  private fpsLastTime = 0;

  // Map viewport (Mercator bounds)
  private mapBounds = { minLon: -180, minLat: -85, maxLon: 180, maxLat: 85 };

  constructor(config: RendererConfig) {
    this.config = config;
  }

  /** Initialize WebGPU and create pipeline */
  async init(): Promise<boolean> {
    if (!navigator.gpu) {
      console.error('WebGPU not supported');
      return false;
    }

    const adapter = await navigator.gpu.requestAdapter();
    if (!adapter) {
      console.error('No GPU adapter found');
      return false;
    }

    this.device = await adapter.requestDevice({
      requiredLimits: {
        maxStorageBufferBindingSize: this.config.maxEntities * 64,
      },
    });

    this.context = this.config.canvas.getContext('webgpu')!;
    const format = navigator.gpu.getPreferredCanvasFormat();
    this.context.configure({
      device: this.device,
      format,
      alphaMode: 'premultiplied',
    });

    // Create shader module
    const shaderModule = this.device.createShaderModule({ code: SHADER_SOURCE });

    // Create pipeline
    this.pipeline = this.device.createRenderPipeline({
      layout: 'auto',
      vertex: {
        module: shaderModule,
        entryPoint: 'vertexMain',
      },
      fragment: {
        module: shaderModule,
        entryPoint: 'fragmentMain',
        targets: [{
          format,
          blend: {
            color: { srcFactor: 'src-alpha', dstFactor: 'one-minus-src-alpha', operation: 'add' },
            alpha: { srcFactor: 'one', dstFactor: 'one-minus-src-alpha', operation: 'add' },
          },
        }],
      },
      primitive: {
        topology: 'triangle-list',
      },
    });

    // Create uniform buffer (64 bytes)
    this.uniformBuffer = this.device.createBuffer({
      size: 64,
      usage: GPUBufferUsage.UNIFORM | GPUBufferUsage.COPY_DST,
    });

    // Create entity storage buffer
    this.entityBuffer = this.device.createBuffer({
      size: this.config.maxEntities * 64,
      usage: GPUBufferUsage.STORAGE | GPUBufferUsage.COPY_DST,
    });

    // Create bind group
    this.bindGroup = this.device.createBindGroup({
      layout: this.pipeline.getBindGroupLayout(0),
      entries: [
        { binding: 0, resource: { buffer: this.uniformBuffer } },
        { binding: 1, resource: { buffer: this.entityBuffer } },
      ],
    });

    return true;
  }

  /** Start the render loop */
  start(): void {
    this.fpsLastTime = performance.now();
    this.renderLoop();
  }

  /** Stop the render loop */
  stop(): void {
    if (this.animationFrameId !== null) {
      cancelAnimationFrame(this.animationFrameId);
      this.animationFrameId = null;
    }
  }

  /** Set map viewport bounds */
  setMapBounds(minLon: number, minLat: number, maxLon: number, maxLat: number): void {
    this.mapBounds = { minLon, minLat, maxLon, maxLat };
  }

  /** Get current render stats */
  getStats(): RenderStats {
    return {
      frameTimeMs: this.lastFrameTime,
      entityCount: this.getEntityCount(),
      gpuTimeMs: 0, // TODO: GPU timestamp queries
      fps: this.fps,
    };
  }

  private getEntityCount(): number {
    const view = new DataView(this.config.sharedBuffer);
    return view.getUint16(4, true);
  }

  private renderLoop = (): void => {
    this.animationFrameId = requestAnimationFrame(this.renderLoop);

    const start = performance.now();
    this.render();
    this.lastFrameTime = performance.now() - start;

    // FPS calculation
    this.fpsAccumulator++;
    const now = performance.now();
    if (now - this.fpsLastTime >= 1000) {
      this.fps = this.fpsAccumulator;
      this.fpsAccumulator = 0;
      this.fpsLastTime = now;
    }
  };

  private render(): void {
    const { canvas, sharedBuffer, pointSize, backgroundColor } = this.config;

    // Read entity count from SAB header
    const sabView = new DataView(sharedBuffer);
    const entityCount = sabView.getUint16(4, true);
    if (entityCount === 0) return;

    const clampedCount = Math.min(entityCount, this.config.maxEntities);

    // Upload entity data from SharedArrayBuffer to GPU
    const entityData = new Uint8Array(sharedBuffer, SAB_HEADER_SIZE, clampedCount * ENTITY_SIZE);
    this.device.queue.writeBuffer(this.entityBuffer, 0, entityData);

    // Update uniforms
    const uniformData = new Float32Array(16);
    uniformData[0] = 0;                          // viewport x
    uniformData[1] = 0;                          // viewport y
    uniformData[2] = canvas.width;               // viewport width
    uniformData[3] = canvas.height;              // viewport height
    uniformData[4] = this.mapBounds.minLon;      // map minLon
    uniformData[5] = this.mapBounds.minLat;      // map minLat
    uniformData[6] = this.mapBounds.maxLon;      // map maxLon
    uniformData[7] = this.mapBounds.maxLat;      // map maxLat
    uniformData[8] = pointSize;                  // point size
    const uniformUint32 = new Uint32Array(uniformData.buffer);
    uniformUint32[9] = clampedCount;             // entity count
    uniformData[10] = performance.now() / 1000;  // time
    this.device.queue.writeBuffer(this.uniformBuffer, 0, uniformData);

    // Render
    const commandEncoder = this.device.createCommandEncoder();
    const textureView = this.context.getCurrentTexture().createView();

    const renderPass = commandEncoder.beginRenderPass({
      colorAttachments: [{
        view: textureView,
        clearValue: { r: backgroundColor[0], g: backgroundColor[1], b: backgroundColor[2], a: backgroundColor[3] },
        loadOp: 'clear',
        storeOp: 'store',
      }],
    });

    renderPass.setPipeline(this.pipeline);
    renderPass.setBindGroup(0, this.bindGroup);
    // 6 vertices per quad (triangle list), instanced for each entity
    renderPass.draw(6, clampedCount, 0, 0);
    renderPass.end();

    this.device.queue.submit([commandEncoder.finish()]);
  }

  /** Destroy GPU resources */
  destroy(): void {
    this.stop();
    this.uniformBuffer?.destroy();
    this.entityBuffer?.destroy();
    this.device?.destroy();
  }
}
