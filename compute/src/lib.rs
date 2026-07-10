//! StreamGrid Compute
//!
//! Spatial indexing, partitioning, and GPU compute acceleration.

pub use streamgrid_protocol::EntityState;

/// A simple grid-based spatial index for entity lookup by region.
pub struct SpatialGrid {
    cell_size_deg: f64,
    grid_width: u32,
    /// cells[cell_id] = list of entity indices in that cell
    cells: Vec<Vec<u32>>,
}

impl SpatialGrid {
    /// Create a new spatial grid with the given cell size in degrees.
    pub fn new(cell_size_deg: f64) -> Self {
        let grid_width = (360.0 / cell_size_deg).ceil() as u32;
        let grid_height = (180.0 / cell_size_deg).ceil() as u32;
        let total_cells = (grid_width * grid_height) as usize;
        Self {
            cell_size_deg,
            grid_width,
            cells: vec![Vec::new(); total_cells],
        }
    }

    /// Clear all entities from the index.
    pub fn clear(&mut self) {
        for cell in &mut self.cells {
            cell.clear();
        }
    }

    /// Insert an entity into the grid based on its position.
    pub fn insert(&mut self, entity_index: u32, latitude: f64, longitude: f64) {
        let cell_id = self.compute_cell(latitude, longitude);
        if (cell_id as usize) < self.cells.len() {
            self.cells[cell_id as usize].push(entity_index);
        }
    }

    /// Query all entity indices within the given bounding box.
    pub fn query(&self, min_lat: f64, min_lon: f64, max_lat: f64, max_lon: f64) -> Vec<u32> {
        let min_cx = ((min_lon + 180.0) / self.cell_size_deg).floor() as u32;
        let max_cx = ((max_lon + 180.0) / self.cell_size_deg).floor() as u32;
        let min_cy = ((min_lat + 90.0) / self.cell_size_deg).floor() as u32;
        let max_cy = ((max_lat + 90.0) / self.cell_size_deg).floor() as u32;

        let mut results = Vec::new();
        for cy in min_cy..=max_cy {
            for cx in min_cx..=max_cx {
                let cell_id = (cy * self.grid_width + cx) as usize;
                if cell_id < self.cells.len() {
                    results.extend_from_slice(&self.cells[cell_id]);
                }
            }
        }
        results
    }

    fn compute_cell(&self, latitude: f64, longitude: f64) -> u32 {
        let cell_x = ((longitude + 180.0) / self.cell_size_deg).floor() as u32;
        let cell_y = ((latitude + 90.0) / self.cell_size_deg).floor() as u32;
        cell_y * self.grid_width + cell_x
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_spatial_grid_insert_query() {
        let mut grid = SpatialGrid::new(1.0);

        // Insert entity at London
        grid.insert(0, 51.5, -0.1);
        // Insert entity at Paris
        grid.insert(1, 48.8, 2.3);
        // Insert entity at New York
        grid.insert(2, 40.7, -74.0);

        // Query Europe region
        let results = grid.query(45.0, -5.0, 55.0, 10.0);
        assert!(results.contains(&0)); // London
        assert!(results.contains(&1)); // Paris
        assert!(!results.contains(&2)); // New York not in Europe
    }

    #[test]
    fn test_spatial_grid_clear() {
        let mut grid = SpatialGrid::new(1.0);
        grid.insert(0, 51.5, -0.1);

        let results = grid.query(50.0, -2.0, 53.0, 2.0);
        assert_eq!(results.len(), 1);

        grid.clear();
        let results = grid.query(50.0, -2.0, 53.0, 2.0);
        assert_eq!(results.len(), 0);
    }
}
