#!/bin/bash
# Performance measurement script for caddy_media_gallery
# Usage: ./perf_measure.sh <label>
# Records benchmark results + a timestamp to performance_log.txt

set -e
LABEL="${1:-unnamed}"
cd /home/osmanj/projects/caddy_media_gallery
PATH="$PATH:/home/osmanj/go/bin"

TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "Measuring: $LABEL ($TIMESTAMP)"

# Run benchmarks and capture ns/op
RESULT=$(go test -bench=BenchmarkRenderPage -benchtime=5s -count=3 -run=^$ 2>&1)

# Extract the relevant lines
BENCH_LINES=$(echo "$RESULT" | grep "^Benchmark")

LOG="/home/osmanj/projects/caddy_media_gallery/performance_log.txt"

# Append to log
{
    echo ""
    echo "=== $LABEL ==="
    echo "Date: $TIMESTAMP"
    echo "$BENCH_LINES"
} >> "$LOG"

cat "$LOG"

# Show summary
echo ""
echo "=== Latest run summary (best of 3 runs each) ==="
tail -n 8 "$LOG" | grep "ns/op" | awk '{print $1, $3}' | sort -k2 -n | head -2
