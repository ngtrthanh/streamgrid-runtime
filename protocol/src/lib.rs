//! StreamGrid Binary Protocol
//!
//! Zero-copy binary encoding/decoding for spatial entity state transport.
//! The canonical entity state is a 64-byte fixed-size struct aligned to cache lines.

use bytemuck::{Pod, Zeroable};

/// Canonical entity state record — 64 bytes, cache-line aligned.
///
/// This is the transport-independent representation of a spatial entity.
/// It can be directly mapped into SharedArrayBuffer for zero-copy browser rendering.
#[repr(C, align(64))]
#[derive(Debug, Clone, Copy, PartialEq, Pod, Zeroable)]
pub struct EntityState {
    /// Unique entity identifier
    pub entity_id: u32,
    /// Status flags (see protocol spec for bit layout)
    pub flags: u16,
    /// Entity type category
    pub entity_type: u8,
    /// Alignment padding
    pub _padding: u8,
    /// Timestamp in milliseconds since Unix epoch
    pub timestamp_ms: u64,
    /// WGS84 latitude in degrees
    pub latitude: f64,
    /// WGS84 longitude in degrees
    pub longitude: f64,
    /// Altitude in meters above WGS84 ellipsoid
    pub altitude_m: f32,
    /// Ground speed in meters per second
    pub speed_ms: f32,
    /// True heading in degrees (0-360)
    pub heading_deg: f32,
    /// Vertical rate in meters per second
    pub vrate_ms: f32,
    /// Update sequence number
    pub sequence: u32,
    /// Pre-computed spatial grid cell ID
    pub grid_cell: u32,
    /// Reserved for future use
    pub _reserved: [u8; 8],
}

const _: () = assert!(std::mem::size_of::<EntityState>() == 64);
const _: () = assert!(std::mem::align_of::<EntityState>() == 64);

/// Frame header — 16 bytes preceding entity state array.
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Pod, Zeroable)]
pub struct FrameHeader {
    /// Magic bytes: 0x53475246 ("SGRF")
    pub magic: u32,
    /// Protocol version
    pub version: u8,
    /// Frame type: 0=full, 1=delta
    pub frame_type: u8,
    /// Number of entities in this frame
    pub entity_count: u16,
    /// Frame timestamp in milliseconds since Unix epoch
    pub timestamp_ms: u64,
}

const _: () = assert!(std::mem::size_of::<FrameHeader>() == 16);

/// Magic bytes for frame identification
pub const FRAME_MAGIC: u32 = 0x5347_5246; // "SGRF" in little-endian

/// Protocol version
pub const PROTOCOL_VERSION: u8 = 1;

/// Entity type constants
pub mod entity_type {
    pub const UNKNOWN: u8 = 0x00;
    pub const AIRCRAFT: u8 = 0x01;
    pub const VESSEL: u8 = 0x02;
    pub const VEHICLE: u8 = 0x03;
    pub const PERSON: u8 = 0x04;
    pub const DRONE: u8 = 0x05;
    pub const SATELLITE: u8 = 0x06;
    pub const SENSOR: u8 = 0x07;
    pub const ROBOT: u8 = 0x08;
    pub const ASSET: u8 = 0x09;
}

/// Flag bit constants
pub mod flags {
    pub const ACTIVE: u16 = 1 << 0;
    pub const POSITION_VALID: u16 = 1 << 1;
    pub const ALTITUDE_VALID: u16 = 1 << 2;
    pub const SPEED_VALID: u16 = 1 << 3;
    pub const HEADING_VALID: u16 = 1 << 4;
    pub const VRATE_VALID: u16 = 1 << 5;
}

/// Encode a slice of EntityState into raw bytes (zero-copy cast).
pub fn encode_frame(header: &FrameHeader, entities: &[EntityState]) -> Vec<u8> {
    let header_bytes = bytemuck::bytes_of(header);
    let entity_bytes = bytemuck::cast_slice::<EntityState, u8>(entities);
    let mut buf = Vec::with_capacity(header_bytes.len() + entity_bytes.len());
    buf.extend_from_slice(header_bytes);
    buf.extend_from_slice(entity_bytes);
    buf
}

/// Decode a frame from raw bytes. Returns header and entity vector.
///
/// Note: Because EntityState requires 64-byte alignment, we cannot always
/// do a zero-copy cast from an arbitrary byte buffer. When the source buffer
/// is properly aligned (e.g., SharedArrayBuffer), use `decode_frame_aligned` instead.
pub fn decode_frame(data: &[u8]) -> Option<(FrameHeader, Vec<EntityState>)> {
    if data.len() < std::mem::size_of::<FrameHeader>() {
        return None;
    }

    let (header_bytes, entity_bytes) = data.split_at(std::mem::size_of::<FrameHeader>());
    let header: &FrameHeader = bytemuck::from_bytes(header_bytes);

    if header.magic != FRAME_MAGIC {
        return None;
    }

    let entity_count = header.entity_count as usize;
    let entity_size = std::mem::size_of::<EntityState>();
    let expected_size = entity_count * entity_size;
    if entity_bytes.len() < expected_size {
        return None;
    }

    // Copy entities to ensure proper alignment
    let mut entities = Vec::with_capacity(entity_count);
    for i in 0..entity_count {
        let offset = i * entity_size;
        let mut entity = EntityState::zeroed();
        let dst = bytemuck::bytes_of_mut(&mut entity);
        dst.copy_from_slice(&entity_bytes[offset..offset + entity_size]);
        entities.push(entity);
    }

    Some((*header, entities))
}

/// Decode a frame from a properly aligned buffer (zero-copy).
///
/// # Safety
/// The caller must ensure `data` is aligned to at least 64 bytes starting
/// from byte 16 (after the frame header).
pub unsafe fn decode_frame_aligned(data: &[u8]) -> Option<(&FrameHeader, &[EntityState])> {
    if data.len() < std::mem::size_of::<FrameHeader>() {
        return None;
    }

    let (header_bytes, entity_bytes) = data.split_at(std::mem::size_of::<FrameHeader>());
    let header: &FrameHeader = bytemuck::from_bytes(header_bytes);

    if header.magic != FRAME_MAGIC {
        return None;
    }

    let entity_count = header.entity_count as usize;
    let expected_size = entity_count * std::mem::size_of::<EntityState>();
    if entity_bytes.len() < expected_size {
        return None;
    }

    let entities: &[EntityState] =
        unsafe { std::slice::from_raw_parts(entity_bytes.as_ptr() as *const EntityState, entity_count) };
    Some((header, entities))
}

/// Compute spatial grid cell for a given lat/lon with specified cell size in degrees.
pub fn compute_grid_cell(latitude: f64, longitude: f64, cell_size_deg: f64) -> u32 {
    let cell_x = ((longitude + 180.0) / cell_size_deg).floor() as u32;
    let cell_y = ((latitude + 90.0) / cell_size_deg).floor() as u32;
    let grid_width = (360.0 / cell_size_deg).ceil() as u32;
    cell_y * grid_width + cell_x
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_entity_state_size() {
        assert_eq!(std::mem::size_of::<EntityState>(), 64);
    }

    #[test]
    fn test_frame_header_size() {
        assert_eq!(std::mem::size_of::<FrameHeader>(), 16);
    }

    #[test]
    fn test_encode_decode_roundtrip() {
        let header = FrameHeader {
            magic: FRAME_MAGIC,
            version: PROTOCOL_VERSION,
            frame_type: 0,
            entity_count: 2,
            timestamp_ms: 1700000000000,
        };

        let entities = vec![
            EntityState {
                entity_id: 1,
                flags: flags::ACTIVE | flags::POSITION_VALID,
                entity_type: entity_type::AIRCRAFT,
                _padding: 0,
                timestamp_ms: 1700000000000,
                latitude: 51.5074,
                longitude: -0.1278,
                altitude_m: 10000.0,
                speed_ms: 250.0,
                heading_deg: 90.0,
                vrate_ms: 0.0,
                sequence: 1,
                grid_cell: compute_grid_cell(51.5074, -0.1278, 1.0),
                _reserved: [0; 8],
            },
            EntityState {
                entity_id: 2,
                flags: flags::ACTIVE | flags::POSITION_VALID,
                entity_type: entity_type::VESSEL,
                _padding: 0,
                timestamp_ms: 1700000000000,
                latitude: 40.7128,
                longitude: -74.0060,
                altitude_m: 0.0,
                speed_ms: 5.0,
                heading_deg: 180.0,
                vrate_ms: 0.0,
                sequence: 1,
                grid_cell: compute_grid_cell(40.7128, -74.0060, 1.0),
                _reserved: [0; 8],
            },
        ];

        let data = encode_frame(&header, &entities);
        assert_eq!(data.len(), 16 + 2 * 64);

        let (decoded_header, decoded_entities) = decode_frame(&data).unwrap();
        assert_eq!(decoded_header, header);
        assert_eq!(decoded_entities, entities);
    }

    #[test]
    fn test_grid_cell_computation() {
        // London (51.5, -0.1) with 1-degree cells
        let cell = compute_grid_cell(51.5, -0.1, 1.0);
        let expected_x = ((-0.1_f64 + 180.0) / 1.0).floor() as u32; // 179
        let expected_y = ((51.5_f64 + 90.0) / 1.0).floor() as u32; // 141
        assert_eq!(cell, expected_y * 360 + expected_x);
    }

    #[test]
    fn test_invalid_frame_magic() {
        let data = vec![0u8; 100];
        assert!(decode_frame(&data).is_none());
    }

    #[test]
    fn test_frame_too_short() {
        let data = vec![0u8; 4];
        assert!(decode_frame(&data).is_none());
    }
}
