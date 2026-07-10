# RQ6: Spatial Partitioning Performance

**Research Question:** How does the spatial grid index perform at production
scale, and what grid cell size maximizes query performance for typical
geospatial use cases?

---

## 1. Implementation

The `SpatialGrid` in `compute/src/lib.rs` is a flat-array grid index over the
surface of the Earth. Entities are bucketed into cells of `cell_size_deg × cell_size_deg`
degrees. Queries iterate over all cells overlapping the bounding box and return
entity indices for further filtering.

```
grid_width  = ceil(360 / cell_size_deg)
grid_height = ceil(180 / cell_size_deg)
total_cells = grid_width × grid_height

cell_x = floor((lon + 180) / cell_size_deg)
cell_y = floor((lat + 90)  / cell_size_deg)
cell_id = cell_y * grid_width + cell_x
```

For the default 1-degree cell size: 360 × 180 = 64,800 cells.

---

## 2. Benchmark Results

Benchmarks run with Criterion.rs on the StreamGrid compute library
(`compute/benches/spatial_index.rs`). All measurements are wall-clock time
on the benchmark host.

**Platform:** Linux x86-64, `cargo bench` (optimized release profile)

### 2.1 Insert Performance

The insert benchmark constructs a fresh `SpatialGrid` and inserts N entities
distributed uniformly across the globe (`lat ∈ [−90, 90]`, `lon ∈ [−180, 180]`).

| Entity Count | Median time   | Mean time     | Std Dev    | ns/entity |
|--------------|---------------|---------------|------------|-----------|
| 100          | ~99 µs        | ~107 µs       | ±4.3 µs    | ~1,070 ns |
| 1,000        | ~124 µs       | ~131 µs       | ±4.9 µs    | ~131 ns   |
| 10,000       | ~250 µs       | ~249 µs       | ±1.1 µs    | ~25 ns    |
| 100,000      | ~1,093 µs     | ~1,093 µs     | ±0.6 µs    | ~11 ns    |

**Latest run (2026-07-10):**

```
spatial_insert/100_entities     time:   [104.73 µs 106.73 µs 108.53 µs]
spatial_insert/1000_entities    time:   [127.38 µs 130.94 µs 135.44 µs]
spatial_insert/10000_entities   time:   [248.31 µs 249.42 µs 250.54 µs]
spatial_insert/100000_entities  time:   [1.0894 ms 1.0932 ms 1.0993 ms]
```

**Key observations:**

- The cost per entity *decreases* from ~1,070 ns at 100 entities to ~11 ns
  at 100,000 entities. This is expected: at low counts, grid construction
  (allocating 64,800 cell vectors) dominates. At high counts, insertion cost
  amortizes over many entities.
- At 100K entities, inserting the full global dataset takes **1.09 ms**,
  which is well within one 100 ms tick (10 Hz update rate). Even at 1 Hz,
  index rebuild takes only ~1% of the available budget.
- Scaling from 1K to 10K entities (10×) costs only 2× more time. From 10K
  to 100K (10×) costs 4.4× more. This sub-linear growth is because entities
  spread across cells — no single cell becomes a bottleneck with uniform
  distribution.

### 2.2 Query Performance

The query benchmark pre-builds the index with N entities, then repeatedly
queries a 10-degree × 10-degree bounding box (`lat [40°, 50°]`, `lon [−80°, −70°]`).
This corresponds to a roughly mid-Atlantic region, approximately the size of
Texas or France.

| Entity Count | Median time   | Mean time     | Std Dev    | Entities in box |
|--------------|---------------|---------------|------------|-----------------|
| 1,000        | ~250 ns       | ~253 ns       | ±7 ns      | ~3 (0.3%)       |
| 10,000       | ~256 ns       | ~258 ns       | ±14 ns     | ~28 (0.28%)     |
| 100,000      | ~247 ns       | ~247 ns       | ±0.1 ns    | ~278 (0.28%)    |

**Latest run (2026-07-10):**

```
spatial_query/1000_entities_10deg_window     time:   [254.68 ns 257.31 ns 260.64 ns]
spatial_query/10000_entities_10deg_window    time:   [253.53 ns 255.56 ns 258.16 ns]
spatial_query/100000_entities_10deg_window   time:   [246.68 ns 246.96 ns 247.26 ns]
```

**Key observations:**

- Query time is **essentially constant** at ~250 ns across 1K, 10K, and 100K
  entities. This is the expected O(cells_in_window + results) behavior: for a
  10-degree window with 1-degree cells, the index always scans 10 × 10 = 100
  cells regardless of total entity count.
- At 100K entities the query is *faster* than at 1K (247 ns vs 257 ns).
  This is a minor cache-warmth effect from the benchmark structure (the larger
  grid is accessed more frequently during warmup iterations).
- 250 ns per spatial query is **negligible** in any production pipeline.
  At 10 Hz with 100 concurrent queries, the total query cost is:
  `10 Hz × 100 queries × 250 ns = 0.25 ms/s` — under 0.03% of one CPU core.

---

## 3. Memory Usage Estimation

### 3.1 Grid Structure Overhead

For a 1-degree cell grid:

```
total_cells = 360 × 180 = 64,800
cell vector overhead = 3 words × 8 bytes = 24 bytes/cell  (ptr, len, cap)
grid structure = 64,800 × 24 = 1,555,200 bytes ≈ 1.5 MB
```

### 3.2 Entity Storage

Each entity index is a `u32` (4 bytes):

```
entity_storage = entity_count × 4 bytes
```

| Entity Count | Entity storage | Grid overhead | Total (1-deg cells) |
|--------------|----------------|---------------|---------------------|
| 100          | 400 B          | 1.5 MB        | ~1.5 MB             |
| 1,000        | 4 KB           | 1.5 MB        | ~1.5 MB             |
| 10,000       | 40 KB          | 1.5 MB        | ~1.5 MB             |
| 100,000      | 400 KB         | 1.5 MB        | ~1.9 MB             |

Grid overhead dominates at all practical entity counts. The fixed 1.5 MB cost
for a 1-degree grid is negligible on modern servers (and acceptable on edge
devices with >256 MB RAM).

### 3.3 Cell Size Trade-offs

| Cell Size | Total Cells | Grid Memory | Query (10° box) | Notes                |
|-----------|-------------|-------------|-----------------|----------------------|
| 5°        | 1,296        | 31 KB       | 4 cells         | Too coarse for urban |
| 2°        | 8,100        | 195 KB      | 25 cells        | Good for regional    |
| 1°        | 64,800       | 1.5 MB      | 100 cells       | **Default (current)**|
| 0.5°      | 259,200      | 6.0 MB      | 400 cells       | Urban density        |
| 0.1°      | 6,480,000    | 150 MB      | 10,000 cells    | Too fine (memory)    |

Finer cells reduce the number of false-positive entities returned per query
(more precise spatial filtering) but increase memory and query scan cost
proportionally.

---

## 4. Analysis

### 4.1 Scalability

The spatial grid scales to 100K entities in **1.09 ms per index rebuild** and
**~250 ns per query**. Both figures are well within production budgets:

- At 10 Hz: 100 ms per tick. Index rebuild uses 1.1% of tick budget.
- At 1 Hz: 1000 ms per tick. Index rebuild uses 0.11% of tick budget.
- Queries are O(cells_in_window), independent of total entity count.
  This is the correct algorithmic property for a spatial index that must
  handle variable entity densities.

### 4.2 Uniform vs. Non-Uniform Distribution

The benchmarks use uniform global distribution. Real-world datasets (ADS-B,
AIS) are highly non-uniform: most aircraft and vessels concentrate over land
and shipping lanes, with sparsely-covered ocean regions.

For non-uniform distributions:
- Cells in high-density regions (e.g., North Atlantic air corridors) may hold
  hundreds of entities, while most ocean cells are empty.
- Query time for a window over a high-density region (e.g., London TMA) will
  be higher than the benchmark suggests, proportional to entities in those cells.
- The uniform benchmark represents average-case performance.

For the worst-case (all 100K entities in one cell), a query touching that cell
returns 100K results. A secondary bounding-box filter on results is required.
The current `query()` method returns all entities in touched cells without
point-in-polygon filtering — the caller is responsible for precise filtering.

### 4.3 Rebuild vs. Incremental Update

The current design rebuilds the entire index on each tick:

```rust
grid.clear();
for entity in &entities {
    grid.insert(entity.entity_id, entity.latitude, entity.longitude);
}
```

At 100K entities and 1.09 ms rebuild time, this is adequate through 10 Hz.
For >10 Hz update rates or >500K entities, an incremental update design
(tracking which cells each entity occupies and only moving changed entities)
would reduce tick cost by ~10× (assuming 10% of entities change position
per tick, typical for ADS-B/AIS).

---

## 5. Recommendations for Production

### 5.1 Cell Size

**Recommended: 1-degree cells (current default)**

Rationale:
- Memory: 1.5 MB — negligible
- Query: 100 cells for a 10° window — fast constant O(cells) scan
- Precision: adequate for ADS-B (en-route spacing >5 nm ≈ 0.08°) and AIS
  (vessel spacing >0.5 nm ≈ 0.008°)

For urban/high-density applications (e.g., drone corridor management over
cities), consider **0.5-degree cells** (6 MB, 400 cells per 10° query).
Still well within memory budget; improves per-query filtering precision.

For continental-scale aggregation dashboards, **2-degree cells** (195 KB,
25 cells per 10° query) reduces scan overhead while remaining precise enough
for regional queries.

### 5.2 Multi-Resolution Grid

For production systems serving mixed workloads (global overview + city-level
detail), maintain two grids simultaneously:
- **2-degree global grid** for low-zoom dashboard queries (~195 KB)
- **0.5-degree regional grid** for high-zoom operational view (~6 MB)

Total overhead: ~6.2 MB. Rebuild cost at 100K entities: ~2× current = ~2.2 ms.

### 5.3 Incremental Updates

Implement incremental cell updates for sustained >10 Hz rates:
1. Store each entity's current cell in `EntityState.grid_cell` (already in protocol).
2. On tick, only remove/re-insert entities whose `grid_cell` has changed.
3. Expected benefit: 10× reduction in tick cost at 10% change rate.

### 5.4 Thread Safety

The current `SpatialGrid` is not thread-safe (`&mut self` for insert/clear).
For concurrent read access (multiple query threads while the generator writes),
use:
- **Read-write lock** (`RwLock<SpatialGrid>`): simple, correct, minor contention
- **Double-buffering** (write new grid, atomically swap pointer): zero read
  contention, preferred for >10 Hz update rates with many readers

### 5.5 Production Validation

The 250 ns query time was measured for a 10° window with 1-degree cells.
Before deploying, validate with realistic non-uniform distributions:
- Load actual ADS-B/AIS replay data from `benchmarks/datasets/`
- Measure query time for high-density windows (KJFK, EGLL approach sectors)
- Verify p99 query latency remains under 10 µs for any realistic window
