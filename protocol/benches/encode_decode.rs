use criterion::{Criterion, black_box, criterion_group, criterion_main};
use streamgrid_protocol::*;

fn make_test_entities(count: usize) -> Vec<EntityState> {
    (0..count)
        .map(|i| EntityState {
            entity_id: i as u32,
            flags: flags::ACTIVE | flags::POSITION_VALID,
            entity_type: entity_type::AIRCRAFT,
            _padding: 0,
            timestamp_ms: 1700000000000 + i as u64,
            latitude: 40.0 + (i as f64 * 0.001),
            longitude: -74.0 + (i as f64 * 0.001),
            altitude_m: 10000.0,
            speed_ms: 250.0,
            heading_deg: 90.0,
            vrate_ms: 0.0,
            sequence: i as u32,
            grid_cell: compute_grid_cell(40.0 + (i as f64 * 0.001), -74.0 + (i as f64 * 0.001), 1.0),
            _reserved: [0; 8],
        })
        .collect()
}

fn bench_encode(c: &mut Criterion) {
    let mut group = c.benchmark_group("encode");

    for count in [100, 1000, 10000] {
        let entities = make_test_entities(count);
        let header = FrameHeader {
            magic: FRAME_MAGIC,
            version: PROTOCOL_VERSION,
            frame_type: 0,
            entity_count: count as u16,
            timestamp_ms: 1700000000000,
        };

        group.bench_function(format!("{count}_entities"), |b| {
            b.iter(|| {
                black_box(encode_frame(&header, &entities));
            });
        });
    }
    group.finish();
}

fn bench_decode(c: &mut Criterion) {
    let mut group = c.benchmark_group("decode");

    for count in [100, 1000, 10000] {
        let entities = make_test_entities(count);
        let header = FrameHeader {
            magic: FRAME_MAGIC,
            version: PROTOCOL_VERSION,
            frame_type: 0,
            entity_count: count as u16,
            timestamp_ms: 1700000000000,
        };
        let data = encode_frame(&header, &entities);

        group.bench_function(format!("{count}_entities"), |b| {
            b.iter(|| {
                let result = decode_frame(&data);
                black_box(result);
            });
        });
    }
    group.finish();
}

criterion_group!(benches, bench_encode, bench_decode);
criterion_main!(benches);
