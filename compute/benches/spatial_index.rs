use criterion::{Criterion, black_box, criterion_group, criterion_main};
use streamgrid_compute::SpatialGrid;

fn bench_spatial_insert(c: &mut Criterion) {
    let mut group = c.benchmark_group("spatial_insert");

    for count in [100, 1000, 10000, 100000] {
        group.bench_function(format!("{count}_entities"), |b| {
            b.iter(|| {
                let mut grid = SpatialGrid::new(1.0);
                for i in 0..count {
                    let lat = -90.0 + (i as f64 / count as f64) * 180.0;
                    let lon = -180.0 + (i as f64 / count as f64) * 360.0;
                    grid.insert(i as u32, lat, lon);
                }
                black_box(&grid);
            });
        });
    }
    group.finish();
}

fn bench_spatial_query(c: &mut Criterion) {
    let mut group = c.benchmark_group("spatial_query");

    for count in [1000, 10000, 100000] {
        let mut grid = SpatialGrid::new(1.0);
        for i in 0..count {
            let lat = -90.0 + (i as f64 / count as f64) * 180.0;
            let lon = -180.0 + (i as f64 / count as f64) * 360.0;
            grid.insert(i as u32, lat, lon);
        }

        group.bench_function(format!("{count}_entities_10deg_window"), |b| {
            b.iter(|| {
                black_box(grid.query(40.0, -80.0, 50.0, -70.0));
            });
        });
    }
    group.finish();
}

criterion_group!(benches, bench_spatial_insert, bench_spatial_query);
criterion_main!(benches);
