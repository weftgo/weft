package clickhouse

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The ClickHouse floor (P9-pre-go-fix3; README.md): 25.6. Migration
// 0001's TTLs are on DateTime64(9) columns, which 25.5 and older refuse
// ("TTL expression result column should have DateTime or Date type, but
// has DateTime64(9)": 23.3, 23.8, 24.8, 25.3, 25.4 and 25.5 tried); 25.6
// passes this package's suite. CI pins 25.8, above it.
const floorMajor, floorMinor = 25, 6

// TestServerAtTheFloor names the floor when a suite runs against an
// older server, rather than failing deep in migration 0001.
func TestServerAtTheFloor(t *testing.T) {
	base := os.Getenv("WEFT_CLICKHOUSE_DSN")
	if base == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set")
	}
	conn := rawConn(t, base)
	defer func() { _ = conn.Close() }()
	var v string
	if err := conn.QueryRow(context.Background(), `SELECT version()`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("version() = %q", v)
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	if major < floorMajor || (major == floorMajor && minor < floorMinor) {
		t.Errorf("ClickHouse %s is below obsdb/clickhouse's floor %d.%d (DateTime64 TTLs, migration 0001)", v, floorMajor, floorMinor)
	}
}
