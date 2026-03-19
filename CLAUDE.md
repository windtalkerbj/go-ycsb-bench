# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

go-ycsb is a Go port of the Yahoo! Cloud Serving Benchmark (YCSB). It's a benchmarking tool for evaluating database performance with support for 20+ databases including MySQL, TiDB, TiKV, PostgreSQL, MongoDB, Redis, Cassandra, Spanner, and more.

## Building and Running

### Build the Project

```bash
make
```

The binary will be created at `./bin/go-ycsb`.

**Build Notes:**
- Build tags are automatically detected based on installed libraries
- FoundationDB support requires client library 6.2.11+ installed
- RocksDB support requires RocksDB to be installed
- SQLite support is conditionally included based on library detection
- Cross-compilation disables CGO-dependent databases (RocksDB, SQLite)

### Run Commands

```bash
# Load data with a workload
./bin/go-ycsb load <dbname> -P workloads/workloada

# Run benchmark
./bin/go-ycsb run <dbname> -P workloads/workloada

# Count keys in database
./bin/go-ycsb count <dbname> -P workloads/workloada --threads 10

# Clean (delete) all keys in a table
./bin/go-ycsb clean <dbname> -P workloads/workloada --confirm

# Interactive shell mode
./bin/go-ycsb shell <dbname>
```

Pass database-specific configurations with `-p field=value`:
```bash
./bin/go-ycsb load mysql -P workloads/workloada -p mysql.host=127.0.0.1 -p mysql.port=3306
```

### Testing

```bash
# Run tests for specific packages
go test ./pkg/util/...
go test ./db/s3/...

# Run all tests
go test ./...
```

### Linting

```bash
make check
```

Uses `golint` on db/, cmd/, and pkg/ directories.

## Architecture

### Core Abstractions

**DB Interface (`pkg/ycsb/db.go`):**
- All database implementations must implement the `ycsb.DB` interface
- Core methods: `Read`, `Scan`, `Update`, `Insert`, `Delete`
- Optional: `BatchDB` interface for batch operations
- Optional: `AnalyzeDB` interface for key distribution analysis
- Thread-aware: `InitThread` and `CleanupThread` for per-goroutine state

**Workload (`pkg/ycsb/workload.go`):**
- Defines operation mix (read/write ratios, access patterns)
- Workload implementations generate operation sequences
- Built-in workloads: A (update heavy), B (read heavy), C (read only), D (read latest), E (scan heavy), F (read-modify-write)

**Generator (`pkg/generator/`):**
- Provides data distribution patterns: Counter, Zipfian, Uniform, Hotspot, ScrambledZipfian, Sequential, SkewedLatest, etc.
- Generators are thread-safe and used for key selection and data generation

### Registration Pattern

All databases and workloads use init-time registration:

```go
// In db/somedb/db.go
func init() {
    ycsb.RegisterDBCreator("somedb", somedbCreator{})
}
```

Main imports all database packages with blank imports to trigger registration:
```go
// In cmd/go-ycsb/main.go
_ "github.com/pingcap/go-ycsb/db/mysql"
_ "github.com/pingcap/go-ycsb/db/tikv"
// ... etc
```

### Key Components

**Client Layer (`pkg/client/`):**
- `worker` struct manages per-goroutine benchmark execution
- Handles throttling (target ops/sec)
- Batch operation support
- Wraps DB implementations with `DbWrapper` for measurement integration

**Measurement (`pkg/measurement/`):**
- Records operation latencies and throughput
- Supports histogram, raw, and CSV output modes
- Integrated via `DbWrapper` which wraps all DB calls

**Properties (`pkg/prop/`):**
- Centralized property definitions and defaults
- Uses `magiconair/properties` library for property file loading
- Properties can be overridden via `-p key=value` command line args

**Commands (`cmd/go-ycsb/`):**
- `load`: Load data into the database
- `run`: Run benchmark workload
- `count`: Count total keys in database (supports concurrent counting)
- `shell`: Interactive command-line interface

### Directory Structure

```
cmd/go-ycsb/     - Main entry point and CLI commands (load, run, shell)
db/              - Database implementations (each in its own subdirectory)
pkg/
  ├── client/    - Benchmark execution client and worker logic
  ├── generator/ - Data distribution generators
  ├── measurement/ - Performance measurement and reporting
  ├── prop/      - Property definitions and constants
  ├── util/      - Utility functions
  ├── workload/  - Workload implementations
  └── ycsb/      - Core interfaces (DB, Workload, etc.)
workloads/       - Workload configuration files
```

## Adding a New Database

1. Create new directory under `db/`
2. Implement `ycsb.DB` interface (optionally `BatchDB`, `AnalyzeDB`, and `CountDB`)
3. Create a DBCreator struct and register in `init()`:
   ```go
   func init() {
       ycsb.RegisterDBCreator("mydb", mydbCreator{})
   }
   ```
4. Add blank import to `cmd/go-ycsb/main.go`
5. Document database-specific properties in README.md

### Optional Interfaces

- **BatchDB**: For batch operations (BatchInsert, BatchRead, BatchUpdate, BatchDelete)
- **AnalyzeDB**: For running database analysis (e.g., ANALYZE TABLE)
- **CountDB**: For efficient key counting (recommended for databases with native COUNT support)

## Workload Files

Workload files in `workloads/` define benchmark parameters:
- `recordcount` - Number of records to load
- `operationcount` - Number of operations to perform
- `workload` - Workload class (default: core)
- Read/write/scan proportions
- Request distribution (uniform, zipfian, latest)
- Field count and sizes

## Common Properties

- `dropdata=true` - Clear database before test
- `verbose=true` - Print executed queries
- `debug.pprof=":6060"` - Go pprof debug server address
- `measurementtype` - "histogram", "raw", or "csv"
- `measurement.output_file` - Output file path (default: stdout)
- `threadcount` - Number of concurrent workers

## Debug Profiling

The application automatically starts a pprof server (default `:6060`). Access at `http://localhost:6060/debug/pprof/` while the benchmark is running.
