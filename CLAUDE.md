# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
go build ./...                                              # Build all packages
CGO_ENABLED=0 go build -o smsc-gateway ./cmd/smsc-gateway   # Static binary
go run ./cmd/smsc-gateway                                   # Run from source

go test ./... -race -count=1 -timeout 300s                  # Full test suite (matches CI)
go test ./smpp/... -v                                       # SMPP library tests only
go test ./gateway/... -v                                    # Gateway tests only
go test -run TestRESTSubmit_Success ./gateway/... -v         # Single test
go test ./e2e/... -v -count=1 -timeout 60s                  # E2E integration tests

go vet ./...                                                # Static analysis
golangci-lint run                                           # Linter (default config, no .golangci.yml)
```

## Architecture

This is an SMPP 3.4 gateway with three core packages and a binary entrypoint:

- **`smpp/`** — Protocol library: PDU codec (`pdu.go`), single transceiver `Client` with auto-reconnect, connection `Pool` with round-robin and windowed flow control, DLR receipt parsing (`handler.go`)
- **`gateway/`** — Embeddable SMSC gateway: northbound SMPP `Server` accepting engine connections, `Router` with MSISDN-sticky affinity and DLR correlation, Pebble-backed `MessageStore` with async batch writes, REST API for SMS submission, Admin API + embedded JS dashboard, Prometheus metrics, `ShardMap` (generic sharded concurrent map), `MTRoute`/`MORoute` tables with failover/round-robin/least-cost strategies, `PoolManager` for named southbound pools
- **`mocksmsc/`** — Mock SMSC server for tests (configurable DLR delay, success rate, MO echo)
- **`cmd/smsc-gateway/`** — Standalone binary orchestrating the full stack with signal-based graceful shutdown
- **`e2e/`** — End-to-end tests exercising the full submit→DLR flow

### Key Data Flows

**MT (Mobile-Terminated):** Engine → Server → Router (assigns gateway msgID, stores MSISDN affinity) → ACK engine → forward worker pool → southbound Pool → SMSC. On submit_sm_resp, stores SMSC msgID → gateway correlation.

**DLR:** SMSC → southbound Pool deliver handler → Router (translates SMSC msgID back to engine msgID via correlation map) → routes to affinity-bound northbound connection → Engine.

**MO (Mobile-Originated):** SMSC → Router → routes to engine that last submitted to that MSISDN.

### Configuration

All runtime config via `GW_*` environment variables (see `gateway/config.go`). Key ports: 2776 (SMPP), 8080 (HTTP/REST/Admin), 9090 (Prometheus).

## Conventions

- **Go version:** 1.26 (matches `go.mod` and CI)
- **Formatting:** `gofmt` (tabs), imports organized by Go toolchain
- **Naming:** Exported `CamelCase`, unexported `camelCase`, short lowercase packages
- **File organization:** Focused by responsibility (`rest_api.go`, `admin_auth.go`, `pool_manager.go`)
- **Test naming:** `TestType_Behavior` (e.g., `TestRESTSubmit_Success`, `TestAdminUser_ValidateJWT`)
- **Commits:** Conventional prefixes (`feat:`, `fix:`, `docs:`), focused and atomic
- **Concurrency patterns:** `atomic.Uint32`/`Uint64`/`Int32`/`Int64` for counters and sequences; `ShardMap` (64-shard FNV-1a) for hot concurrent maps
