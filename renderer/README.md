# Renderer

WebGPU-based renderer for StreamGrid entity visualization.

## Design

- Reads entity positions directly from SharedArrayBuffer
- Instanced rendering for thousands of entities
- Minimal CPU-side work per frame
- Map projection in vertex shader

## Language

TypeScript + WGSL
