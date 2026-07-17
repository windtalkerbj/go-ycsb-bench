# go-ycsb Agent Guide

This file provides essential information for AI coding agents working with the go-ycsb project.

## Project Overview

go-ycsb is a Go port of the [Yahoo! Cloud Serving Benchmark (YCSB)](https://github.com/brianfrankcooper/YCSB). It is a benchmarking tool for evaluating database performance with support for 20+ databases including:

- MySQL / TiDB / MariaDB
- TiKV
- PostgreSQL / CockroachDB / AlloyDB / Yugabyte
- MongoDB
- Redis / Redis Cluster
- Cassandra / ScyllaDB
- FoundationDB
- RocksDB
- Badger
- BoltDB
- Aerospike
- Google Spanner
- SQLite
- etcd
- DynamoDB
- Amazon S3 / S3-compatible
- Elasticsearch
- MinIO
- Pegasus

**Repository:** https://github.com/pingcap/go-ycsb  
**License:** Apache License 2.0  
**Minimum Go Version:** 1.18

## Build and Run Commands

### Build

```bash
# Standard build
make

# Binary location: ./bin/go-ycsb
```

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

# Count keys in database (concurrent counting)
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
# Run all tests
go test ./...

# Run tests for specific packages
go test ./pkg/util/...
go test ./db/s3/...
```

### Linting

```bash
make check
```

Uses `golint` on db/, cmd/, and pkg/ directories.

## Project Structure

```
cmd/go-ycsb/       - Main entry point and CLI commands
  ├── main.go      - Application entry, imports all DB packages
  ├── client.go    - load/run command implementations
  ├── shell.go     - Interactive shell command
  ├── count.go     - Count keys command
  └── clean.go     - Clean/delete data command

db/                - Database implementations (each in own subdirectory)
  ├── basic/       - Debug/test database (prints operations)
  ├── mysql/       - MySQL/TiDB/MariaDB support
  ├── tikv/        - TiKV support
  ├── pg/          - PostgreSQL support
  ├── redis/       - Redis support
  ├── mongodb/     - MongoDB support
  ├── cassandra/   - Cassandra support
  └── ...          - Other database implementations

pkg/
  ├── client/      - Benchmark execution client and worker logic
  ├── generator/   - Data distribution generators
  │                  (Counter, Zipfian, Uniform, Hotspot, etc.)
  ├── measurement/ - Performance measurement and reporting
  ├── prop/        - Property definitions and constants
  ├── util/        - Utility functions
  ├── workload/    - Workload implementations
  └── ycsb/        - Core interfaces (DB, Workload, etc.)

workloads/         - Workload configuration files
  ├── workloada    - Update heavy (50/50 read/update)
  ├── workloadb    - Read heavy (95/5 read/update)
  ├── workloadc    - Read only
  ├── workloadd    - Read latest
  ├── workloade    - Scan heavy
  └── workloadf    - Read-modify-write
```

## Core Architecture

### DB Interface

All database implementations must implement the `ycsb.DB` interface (`pkg/ycsb/db.go`):

```go
type DB interface {
    Close() error
    InitThread(ctx context.Context, threadID int, threadCount int) context.Context
    CleanupThread(ctx context.Context)
    Read(ctx context.Context, table string, key string, fields []string) (map[string][]byte, error)
    Scan(ctx context.Context, table string, startKey string, count int, fields []string) ([]map[string][]byte, error)
    Update(ctx context.Context, table string, key string, values map[string][]byte) error
    Insert(ctx context.Context, table string, key string, values map[string][]byte) error
    Delete(ctx context.Context, table string, key string) error
}
```

Optional interfaces:
- **BatchDB** (`pkg/ycsb/db.go`): For batch operations (BatchInsert, BatchRead, BatchUpdate, BatchDelete)
- **AnalyzeDB** (`pkg/ycsb/db.go`): For running database analysis (e.g., ANALYZE TABLE)
- **CountDB** (`pkg/ycsb/db.go`): For efficient key counting
- **CleanDB** (`pkg/ycsb/db.go`): For cleaning/deleting all keys in a table

### Registration Pattern

All databases use init-time registration:

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

### Workload Interface

Workloads implement `ycsb.Workload` (`pkg/ycsb/workload.go`):

```go
type Workload interface {
    Close() error
    InitThread(ctx context.Context, threadID int, threadCount int) context.Context
    CleanupThread(ctx context.Context)
    Load(ctx context.Context, db DB, totalCount int64) error
    DoInsert(ctx context.Context, db DB) error
    DoBatchInsert(ctx context.Context, batchSize int, db DB) error
    DoTransaction(ctx context.Context, db DB) error
    DoBatchTransaction(ctx context.Context, batchSize int, db DB) error
}
```

### Client Layer

The `pkg/client/` package manages benchmark execution:
- `worker` struct manages per-goroutine execution
- Handles throttling (target ops/sec)
- Supports batch operations
- Wraps DB implementations with `DbWrapper` for measurement integration

### Measurement System

`pkg/measurement/` records operation latencies and throughput:
- Supports histogram, raw, and CSV output modes
- Integrated via `DbWrapper` which wraps all DB calls
- Warm-up period support
- Periodic summary output

## Adding a New Database

1. Create new directory under `db/`
2. Implement `ycsb.DB` interface (optionally `BatchDB`, `AnalyzeDB`, `CountDB`, `CleanDB`)
3. Create a DBCreator struct and register in `init()`:
   ```go
   func init() {
       ycsb.RegisterDBCreator("mydb", mydbCreator{})
   }
   ```
4. Add blank import to `cmd/go-ycsb/main.go`
5. Document database-specific properties in README.md

### Example Database Implementation Structure

```go
package mydb

import (
    "context"
    "github.com/magiconair/properties"
    "github.com/pingcap/go-ycsb/pkg/prop"
    "github.com/pingcap/go-ycsb/pkg/ycsb"
)

type myDB struct {
    // database-specific fields
}

type contextKey string
const stateKey = contextKey("myDB")

type myDBState struct {
    // per-thread state
}

func (db *myDB) InitThread(ctx context.Context, _ int, _ int) context.Context {
    state := &myDBState{}
    return context.WithValue(ctx, stateKey, state)
}

func (db *myDB) CleanupThread(ctx context.Context) {
    // cleanup per-thread resources
}

func (db *myDB) Close() error {
    return nil
}

// Implement Read, Scan, Update, Insert, Delete...

type myDBCreator struct{}

func (c myDBCreator) Create(p *properties.Properties) (ycsb.DB, error) {
    // initialize and return database instance
    return &myDB{}, nil
}

func init() {
    ycsb.RegisterDBCreator("mydb", myDBCreator{})
}
```

## Workload Configuration

Workload files define benchmark parameters:

```
recordcount=1000           # Number of records to load
operationcount=1000        # Number of operations to perform
workload=core              # Workload class

readproportion=0.5         # Read operation proportion
updateproportion=0.5       # Update operation proportion
scanproportion=0.0         # Scan operation proportion
insertproportion=0.0       # Insert operation proportion

requestdistribution=zipfian  # Key distribution: uniform, zipfian, latest
```

## Common Properties

| Property | Default | Description |
|----------|---------|-------------|
| `dropdata` | false | Clear database before test |
| `verbose` | false | Print executed queries |
| `debug.pprof` | ":6060" | Go debug profile address |
| `measurementtype` | "histogram" | Output: histogram, raw, or csv |
| `measurement.output_file` | "" | Output file path (default: stdout) |
| `threadcount` | 200 | Number of concurrent workers |
| `batch.size` | 1 | Batch operation size |
| `warmuptime` | 0 | Warm-up duration in seconds |

## Code Style Guidelines

- All source files include Apache License header
- Use `gofmt` for formatting
- Use `golint` for linting (`make check`)
- Follow Go naming conventions
- Use context for cancellation and timeouts
- Thread-safe implementations required for concurrent access

## CI/CD

GitHub Actions workflows (`.github/workflows/`):

- **go.yml**: Builds for linux/darwin amd64/arm64 on push/PR
- **docker.yml**: Docker image builds
- **github-release-publish.yml**: Release publishing

## Debug Profiling

The application automatically starts a pprof server (default `:6060`). Access at `http://localhost:6060/debug/pprof/` while the benchmark is running.

## Security Considerations

- Database credentials passed via command line or property files
- TLS/SSL support varies by database (check individual implementations)
- pprof endpoint exposes runtime information - secure in production
