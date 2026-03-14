# go-smsc

An SMPP 3.4 library and SMSC gateway for Go.

Three packages, one module:

- **`smpp/`** — SMPP client library with connection pooling, windowed flow control, and TLS
- **`gateway/`** — Embeddable SMSC gateway with MSISDN-sticky routing, store-and-forward, REST API, admin UI, and prefix-based routing
- **`mocksmsc/`** — Mock SMSC server for testing

Plus a standalone binary: **`cmd/smsc-gateway/`**

[![CI](https://github.com/IDNTEQ/go-smsc/actions/workflows/ci.yml/badge.svg)](https://github.com/IDNTEQ/go-smsc/actions/workflows/ci.yml)

## Requirements

- Go 1.26 or later

## Install

```bash
go get github.com/idnteq/go-smsc/smpp      # SMPP library only
go get github.com/idnteq/go-smsc/gateway    # embeddable gateway
go get github.com/idnteq/go-smsc/mocksmsc   # mock SMSC for tests
```

## SMPP Library

A production-grade SMPP 3.4 client with connection pooling, windowed backpressure, automatic reconnection, TLS, and OpenTelemetry instrumentation.

### Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/idnteq/go-smsc/smpp"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewProduction()
    ctx := context.Background()

    // Create a pool of 5 SMPP connections
    cfg := smpp.Config{
        Host:     "smsc.example.com",
        Port:     2775,
        SystemID: "myapp",
        Password: "secret",
    }
    poolCfg := smpp.DefaultPoolConfig()

    pool := smpp.NewPool(cfg, poolCfg, func(src, dst string, esm byte, body []byte) {
        log.Printf("Received deliver_sm from %s", src)
    }, logger)

    if err := pool.Connect(ctx); err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    // Submit a message
    resp, err := pool.Submit(&smpp.SubmitRequest{
        DestAddr:   "+27821234567",
        SourceAddr: "MyApp",
        Payload:    []byte("Hello, world!"),
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Message ID: %s", resp.MessageID)
}
```

### Features

- **Connection pooling** — round-robin across N connections with automatic reconnection
- **Windowed flow control** — per-connection submit windows prevent overloading the SMSC
- **TLS** — optional TLS with custom `tls.Config`
- **DLR parsing** — `ParseDLRReceipt()` handles real-world SMSC receipt format variations
- **OpenTelemetry** — built-in metrics for deliver queue depth, backpressure, and drops
- **Pure Go** — `CGO_ENABLED=0` compatible, no C dependencies

## SMSC Gateway

A standalone SMSC gateway that sits between SMPP clients and upstream SMSCs. Accepts SMPP connections on the northbound side, forwards messages via SMPP pools on the southbound side.

```
 Client 1 ──┐                         ┌──> SMSC (MNO 1)
 Client 2 ──┤── SMPP ──> Gateway ─────┤──> SMSC (MNO 2)
 Client N ──┘             │            └──> Mock SMSC
                    REST API (HTTP)
                    Admin UI
                    Prometheus
```

### Run with Docker

```bash
docker run -p 2776:2776 -p 8080:8080 -p 9090:9090 \
  -e GW_SMSC_HOST=smsc.example.com \
  -e GW_SMSC_PORT=2775 \
  -e GW_SMSC_SYSTEM_ID=myapp \
  -e GW_SMSC_PASSWORD=secret \
  ghcr.io/idnteq/go-smsc:latest
```

### Run from Source

```bash
go run ./cmd/smsc-gateway
```

### Build Static Binary

```bash
CGO_ENABLED=0 go build -o smsc-gateway ./cmd/smsc-gateway
```

### Gateway Features

- **MSISDN-sticky routing** — DLR and MO always route back to the client that submitted
- **Store-and-forward** — Pebble-backed disk persistence, survives restarts
- **Prefix-based MT routing** — route messages to different SMSCs by MSISDN prefix
- **MO routing** — route incoming messages to specific clients by pattern
- **REST API** — HTTP API for SMS submission with DLR webhook callbacks
- **Admin UI** — embedded web dashboard for monitoring and management
- **API key auth** — SHA-256 hashed API keys with per-key rate limits
- **Admin auth** — bcrypt passwords with JWT sessions
- **TLS** — optional TLS for both SMPP and HTTP
- **Prometheus metrics** — 20+ gauges and counters
- **Retry with backoff** — automatic retry for failed southbound submits
- **Rate limiting** — per-connection TPS limits
- **Graceful shutdown** — drain in-flight messages before stopping

### Embed in Your Application

```go
package main

import (
    "context"

    "github.com/idnteq/go-smsc/gateway"
    "github.com/idnteq/go-smsc/smpp"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewProduction()
    ctx := context.Background()

    cfg := gateway.LoadConfig()                        // reads GW_* env vars
    metrics := gateway.NewMetrics()
    store, _ := gateway.NewMessageStore(cfg.DataDir, logger)
    defer store.Close()

    router := gateway.NewRouter(store, metrics, cfg, logger)
    server := gateway.NewServer(cfg, metrics, logger)
    server.SetRouter(router)
    router.SetServer(server)

    // Create southbound SMPP pool
    pool := smpp.NewPool(smpp.Config{
        Host: cfg.SMSCHost, Port: cfg.SMSCPort,
        SystemID: cfg.SMSCSystemID, Password: cfg.SMSCPassword,
    }, smpp.DefaultPoolConfig(), router.HandleDeliver, logger)
    router.SetSouthbound(pool)
    pool.Connect(ctx)
    defer pool.Close()

    server.Start()
    defer server.Stop()

    <-ctx.Done()
}
```

### REST API

Submit SMS via HTTP:

```bash
# Submit a single message
curl -X POST http://localhost:8080/api/v1/sms \
  -H "Authorization: Bearer sk_live_..." \
  -H "Content-Type: application/json" \
  -d '{"to": "+27821234567", "from": "MyApp", "body": "Hello!"}'

# Query message status
curl http://localhost:8080/api/v1/sms/{id} \
  -H "Authorization: Bearer sk_live_..."

# Batch submit
curl -X POST http://localhost:8080/api/v1/sms/batch \
  -H "Authorization: Bearer sk_live_..." \
  -H "Content-Type: application/json" \
  -d '{"messages": [{"to": "+27821234567", "body": "Hello!"}]}'
```

### Configuration

All settings via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `GW_LISTEN_ADDR` | `:2776` | Northbound SMPP listen address |
| `GW_SERVER_PASSWORD` | `password` | Password for SMPP client auth |
| `GW_SMSC_HOST` | `localhost` | Upstream SMSC host |
| `GW_SMSC_PORT` | `2775` | Upstream SMSC port |
| `GW_SMSC_SYSTEM_ID` | `smppclient` | Upstream SMSC system_id |
| `GW_SMSC_PASSWORD` | `password` | Upstream SMSC password |
| `GW_POOL_CONNECTIONS` | `5` | Southbound connections per pool |
| `GW_POOL_WINDOW_SIZE` | `10` | Submit window per connection |
| `GW_DATA_DIR` | `/tmp/smscgw-data` | Pebble data directory |
| `GW_MAX_MESSAGES` | `1000000` | Max stored messages |
| `GW_HTTP_ADDR` | `:8080` | REST API + Admin UI address |
| `GW_METRICS_ADDR` | `:9090` | Prometheus metrics address |
| `GW_TLS_CERT` | | TLS certificate file |
| `GW_TLS_KEY` | | TLS key file |
| `GW_JWT_SECRET` | | JWT signing secret (required for admin auth) |
| `GW_FORWARD_WORKERS` | `64` | Southbound forwarding goroutines |
| `GW_RATE_LIMIT_TPS` | `0` | Max submits/sec per connection (0 = unlimited) |
| `GW_MAX_SUBMIT_RETRIES` | `3` | Retries before synthetic failure DLR |
| `GW_ENQUIRE_LINK_SEC` | `30` | Keep-alive interval for client connections |
| `GW_IDLE_TIMEOUT_SEC` | `120` | Close idle connections after this |
| `GW_DRAIN_TIMEOUT_SEC` | `10` | Graceful shutdown drain timeout |

## Mock SMSC

A test SMSC server for development and integration testing:

```go
import (
    "github.com/idnteq/go-smsc/mocksmsc"
    "go.uber.org/zap"
)

logger, _ := zap.NewDevelopment()
srv := mocksmsc.NewServer(mocksmsc.Config{
    Port:           2775,
    DLRDelayMs:     100,
    DLRSuccessRate: 0.95,
    EnableMO:       true,
    MOPayload:      []byte("response"),
}, logger)

srv.Start()
defer srv.Stop()
```

## Architecture

```
┌──────────────────────────────────────────────────┐
│  smpp/ — SMPP 3.4 Protocol Library               │
│  ├─ Client     TCP + TLS, bind, submit, deliver  │
│  ├─ Pool       N connections, round-robin, window │
│  ├─ PDU        Encode/decode, TLV support         │
│  └─ Handler    DLR receipt parsing                │
├──────────────────────────────────────────────────┤
│  gateway/ — SMSC Gateway                          │
│  ├─ Server        Northbound SMPP listener        │
│  ├─ Connection    Per-session state machine       │
│  ├─ Router        Affinity, correlation, forward  │
│  ├─ Store         Pebble KV (store-and-forward)   │
│  ├─ RouteTable    Prefix-based MT/MO routing      │
│  ├─ PoolManager   Multiple southbound pools       │
│  ├─ REST API      HTTP submit + DLR callbacks     │
│  ├─ Admin API     Management endpoints            │
│  ├─ Admin UI      Embedded web dashboard          │
│  ├─ ShardMap      64-shard concurrent map         │
│  └─ Metrics       Prometheus counters/gauges      │
├──────────────────────────────────────────────────┤
│  mocksmsc/ — Test SMSC Server                     │
│  └─ Configurable DLR delay, success rate, MO      │
└──────────────────────────────────────────────────┘
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
