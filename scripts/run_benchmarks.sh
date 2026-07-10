#!/bin/bash
# StreamGrid Benchmark Runner
# Runs all benchmarks and collects results in benchmarks/results/

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RESULTS_DIR="$PROJECT_ROOT/benchmarks/results"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RUN_DIR="$RESULTS_DIR/run_$TIMESTAMP"

echo "=== StreamGrid Benchmark Suite ==="
echo "Project root: $PROJECT_ROOT"
echo "Results dir:  $RUN_DIR"
echo ""

mkdir -p "$RUN_DIR"

# Capture environment
cat > "$RUN_DIR/environment.json" <<EOF
{
  "timestamp": "$(date -Iseconds)",
  "hostname": "$(hostname)",
  "os": "$(uname -s)",
  "arch": "$(uname -m)",
  "kernel": "$(uname -r)",
  "cpus": $(nproc),
  "memory_kb": $(grep MemTotal /proc/meminfo 2>/dev/null | awk '{print $2}' || echo 0),
  "go_version": "$(go version 2>/dev/null | awk '{print $3}' || echo 'N/A')",
  "rust_version": "$(rustc --version 2>/dev/null | awk '{print $2}' || echo 'N/A')",
  "cargo_version": "$(cargo --version 2>/dev/null | awk '{print $2}' || echo 'N/A')"
}
EOF

echo "Environment captured."
echo ""

# Run Go benchmarks
echo "--- Go Benchmarks ---"
cd "$PROJECT_ROOT"
if go test ./benchmarks/ -v -run TestHarness 2>&1 | tee "$RUN_DIR/go_framework_test.log"; then
    echo "  ✓ Go benchmark framework tests passed"
else
    echo "  ✗ Go benchmark framework tests failed"
fi
echo ""

# Run Rust benchmarks (criterion)
echo "--- Rust Benchmarks ---"
cd "$PROJECT_ROOT"
if cargo bench 2>&1 | tee "$RUN_DIR/rust_benchmarks.log"; then
    echo "  ✓ Rust benchmarks completed"
    # Copy criterion HTML reports if they exist
    if [ -d "target/criterion" ]; then
        cp -r target/criterion "$RUN_DIR/criterion_reports" 2>/dev/null || true
    fi
else
    echo "  ✗ Rust benchmarks failed"
fi
echo ""

echo "=== Benchmark run complete ==="
echo "Results saved to: $RUN_DIR"
ls -la "$RUN_DIR"
