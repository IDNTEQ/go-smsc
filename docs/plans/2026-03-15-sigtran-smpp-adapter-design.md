# SIGTRAN ↔ gRPC Adapter Design

## Context

The go-smsc gateway needs to connect to mobile networks via SS7/SIGTRAN. Rather than using SMPP between the gateway and adapter (which adds an unnecessary protocol translation layer for local/colocated processes), we use **gRPC** for the gateway ↔ adapter interface. This gives us type-safe contracts, bidirectional streaming, sub-millisecond latency, and native support in both Go and Java.

The SS7 adapter uses corsac-jss7 (Mobius fork) for the MAP/TCAP/SCCP/M3UA stack and exposes a gRPC server that the go-smsc gateway connects to as a client.

## Architecture

```
SMPP Clients                 go-smsc Gateway                    SS7 Adapter(s)
───────────                  ───────────────                    ──────────────
engine-za ──┐                                          ┌──→ [gRPC Server]
engine-ng ──┤── SMPP ──→  Gateway  ── gRPC client ─────┤    corsac-jss7
engine-ke ──┘              │                           │    M3UA/SCTP ──→ SS7 Network
                      Admin UI                         │
                      REST API                         └──→ [gRPC Server]
                      Prometheus                            corsac-jss7
                                                            M3UA/SCTP ──→ SS7 Network
```

The adapter is a gRPC server. The gateway connects to it as a gRPC client — the same way it connects to SMPP downstream SMSCs, but using gRPC transport instead. Multiple adapters can run for different networks (one per SS7 interconnect).

## Protobuf Service Definition

```protobuf
syntax = "proto3";
package smsc.v1;

option go_package = "github.com/idnteq/go-smsc/proto/smscv1";
option java_package = "com.idnteq.sigtran.proto";

// SMSAdapterService is implemented by the SS7 adapter (gRPC server).
// The gateway connects as a gRPC client.
service SMSAdapterService {
  // Submit an MT-SMS for delivery via SS7/MAP.
  // Adapter performs SRI-for-SM → MT-ForwardSM.
  // Returns after MAP operation completes (synchronous).
  rpc SubmitMT(SubmitMTRequest) returns (SubmitMTResponse);

  // Open a server-streaming channel for MO-SMS and DLRs.
  // The adapter pushes incoming messages (MO-ForwardSM from network,
  // ReportSMDeliveryStatus, AlertServiceCentre events) to the gateway.
  rpc StreamDeliveries(StreamDeliveriesRequest) returns (stream DeliveryMessage);

  // Query adapter health and SS7 link status.
  rpc GetStatus(GetStatusRequest) returns (AdapterStatus);

  // Cancel a previously submitted MT-SMS (if still queued).
  rpc CancelMT(CancelMTRequest) returns (CancelMTResponse);
}

// -------------------------------------------------------------------
// MT-SMS (Gateway → Adapter → Network)
// -------------------------------------------------------------------

message SubmitMTRequest {
  string message_id = 1;          // Gateway-assigned message ID
  string source_addr = 2;         // Source address / sender ID
  uint32 source_addr_ton = 3;     // TON (0=unknown, 1=international, 5=alphanumeric)
  uint32 source_addr_npi = 4;     // NPI
  string dest_addr = 5;           // Destination MSISDN (E.164)
  uint32 dest_addr_ton = 6;
  uint32 dest_addr_npi = 7;
  bytes payload = 8;              // Message content
  uint32 data_coding = 9;         // 0=GSM7, 8=UCS2, etc.
  bool register_dlr = 10;         // Request delivery report
  uint32 esm_class = 11;          // ESM class
  uint32 protocol_id = 12;        // Protocol ID (GSM)
  uint32 priority = 13;           // Priority flag
  string validity_period = 14;    // Validity period
  map<string, bytes> tlvs = 15;   // Optional TLV parameters
}

message SubmitMTResponse {
  string message_id = 1;          // Adapter/network-assigned message ID
  uint32 status = 2;              // 0 = success, non-zero = error code
  string error_message = 3;       // Human-readable error (if status != 0)
  string imsi = 4;                // Subscriber IMSI (from SRI-for-SM)
  string msc_address = 5;         // Serving MSC GT (from SRI-for-SM)
}

// -------------------------------------------------------------------
// MO-SMS + DLR (Network → Adapter → Gateway)
// -------------------------------------------------------------------

message StreamDeliveriesRequest {
  string adapter_id = 1;          // Adapter identifying itself
  uint32 max_inflight = 2;        // Flow control: max unacknowledged deliveries
}

message DeliveryMessage {
  string delivery_id = 1;         // Adapter-assigned delivery ID (for ack)
  DeliveryType type = 2;

  // For MO_SMS:
  string source_addr = 3;         // Originating MSISDN
  uint32 source_addr_ton = 4;
  uint32 source_addr_npi = 5;
  string dest_addr = 6;           // Destination (our SMSC GT)
  uint32 dest_addr_ton = 7;
  uint32 dest_addr_npi = 8;
  bytes payload = 9;              // Message content
  uint32 data_coding = 10;
  uint32 esm_class = 11;
  uint32 protocol_id = 12;

  // For DLR:
  string original_message_id = 13;  // The message_id from SubmitMTResponse
  string dlr_status = 14;           // DELIVRD, UNDELIV, EXPIRED, etc.
  string dlr_error_code = 15;       // Network error code
  string dlr_done_date = 16;        // Delivery timestamp

  // For ALERT:
  string alert_msisdn = 17;       // Subscriber now reachable

  map<string, bytes> tlvs = 18;   // Optional TLV parameters
}

enum DeliveryType {
  DELIVERY_TYPE_UNSPECIFIED = 0;
  MO_SMS = 1;                     // Mobile-originated SMS
  DLR = 2;                        // Delivery report
  ALERT = 3;                      // AlertServiceCentre (subscriber reachable)
}

// -------------------------------------------------------------------
// Status + Management
// -------------------------------------------------------------------

message GetStatusRequest {}

message AdapterStatus {
  string adapter_id = 1;
  string adapter_version = 2;
  bool healthy = 3;

  // SS7 link status
  repeated LinkStatus links = 4;

  // Counters
  int64 mt_submitted = 5;
  int64 mt_delivered = 6;
  int64 mt_failed = 7;
  int64 mo_received = 8;
  int64 dlr_received = 9;
  int64 active_dialogs = 10;
}

message LinkStatus {
  string name = 1;                // Association name
  string state = 2;              // up, down, inactive, pending
  string peer_address = 3;
  int64 messages_sent = 4;
  int64 messages_received = 5;
}

message CancelMTRequest {
  string message_id = 1;
}

message CancelMTResponse {
  uint32 status = 1;              // 0 = cancelled, non-zero = error
  string error_message = 2;
}
```

## Gateway-Side Changes (Go)

### New: gRPC Bind Type

The gateway's "Bind" concept currently maps to an SMPP Pool. Add a **gRPC bind** option alongside SMPP, so the admin UI shows:

```
Binds
─────
Name            Type    Host              Active  Health
vodacom-smpp    SMPP    smsc.voda.co.za   2/2     Healthy ●
mtn-ss7         gRPC    localhost:9090    1/1     Healthy ●
```

### New files

**`proto/smscv1/adapter.proto`** — the protobuf definition above.

**`proto/smscv1/*.pb.go`** — generated Go code (via `protoc-gen-go` + `protoc-gen-go-grpc`).

**`gateway/grpc_bind.go`** — gRPC client that implements the same interface as `smpp.Pool` from the gateway's perspective:

```go
// GRPCBind connects to a gRPC SMS adapter and provides the same
// submit/deliver interface as an SMPP Pool.
type GRPCBind struct {
    name     string
    conn     *grpc.ClientConn
    client   smscv1.SMSAdapterServiceClient
    stream   smscv1.SMSAdapterService_StreamDeliveriesClient
    handler  DeliverHandler   // same callback type as SMPP pool
    healthy  bool
}

func NewGRPCBind(name string, addr string, handler DeliverHandler) (*GRPCBind, error)
func (b *GRPCBind) Connect(ctx context.Context) error
func (b *GRPCBind) SubmitRaw(body []byte) (*smpp.SubmitResponse, error)  // NOT used for gRPC
func (b *GRPCBind) SubmitMT(req *SubmitMTRequest) (*SubmitMTResponse, error)
func (b *GRPCBind) Close() error
func (b *GRPCBind) ActiveConnections() int
func (b *GRPCBind) IsHealthy() bool
```

**`gateway/grpc_bind_test.go`** — tests with a mock gRPC server.

### Modified files

**`gateway/pool_manager.go`** — Add `BindType` field to `SouthboundPoolConfig`:

```go
type SouthboundPoolConfig struct {
    Name     string
    BindType string  // "smpp" (default) or "grpc"
    // SMPP fields (used when BindType=smpp):
    Host, Port, SystemID, Password, ...
    // gRPC fields (used when BindType=grpc):
    GRPCAddress string  // host:port for gRPC server
    GRPCUseTLS  bool
    GRPCCertFile string
}
```

`Add()` checks `BindType` and creates either an `smpp.Pool` or a `GRPCBind`.

**`gateway/route_table.go`** — The `Resolve()` method returns a pool to submit to. Define a common `Submitter` interface that both `smpp.Pool` and `GRPCBind` implement:

```go
type Submitter interface {
    SubmitRaw(body []byte) (*smpp.SubmitResponse, error)
    ActiveConnections() int
    IsHealthy() bool
    Close() error
}
```

Actually, for gRPC binds, `SubmitRaw` (which takes a raw SMPP PDU body) doesn't make sense. The router should call a higher-level submit method. The interface becomes:

```go
type Submitter interface {
    // Submit sends a message. For SMPP, body is the raw PDU body.
    // For gRPC, the structured fields are used instead.
    Submit(req *SubmitRequest) (*SubmitResponse, error)
    ActiveConnections() int
    IsHealthy() bool
    Close() error
}

type SubmitRequest struct {
    RawBody    []byte  // For SMPP: raw submit_sm PDU body (forwarded as-is)
    SourceAddr string  // Parsed source address
    DestAddr   string  // Parsed destination address
    Payload    []byte  // Message content
    DataCoding byte
    ESMClass   byte
    RegisterDLR bool
    // ... other parsed fields
}
```

The SMPP pool wrapper uses `RawBody` (existing `SubmitRaw` behavior). The gRPC bind uses the structured fields.

**`gateway/router.go`** — Update `forwardSubmitRaw` to use the `Submitter` interface instead of directly calling `pool.SubmitRaw()`. Pass both the raw body AND parsed fields.

**`gateway/admin-ui/app.js`** — Update Binds page to show bind type selector (SMPP or gRPC) and appropriate form fields for each.

## Adapter-Side Changes (Java)

### Replace SMPP Server with gRPC Server

Remove cloudhopper-smpp dependency. Add:

```xml
<dependency>
    <groupId>io.grpc</groupId>
    <artifactId>grpc-netty-shaded</artifactId>
</dependency>
<dependency>
    <groupId>io.grpc</groupId>
    <artifactId>grpc-protobuf</artifactId>
</dependency>
<dependency>
    <groupId>io.grpc</groupId>
    <artifactId>grpc-stub</artifactId>
</dependency>
```

### gRPC Service Implementation

```java
public class SmsAdapterServiceImpl extends SMSAdapterServiceGrpc.SMSAdapterServiceImplBase {

    @Override
    public void submitMT(SubmitMTRequest request, StreamObserver<SubmitMTResponse> observer) {
        // 1. SRI-for-SM to HLR
        // 2. MT-ForwardSM to MSC
        // 3. Return response
    }

    @Override
    public void streamDeliveries(StreamDeliveriesRequest request,
                                  StreamObserver<DeliveryMessage> observer) {
        // Register this observer — push MO/DLR/Alert messages as they arrive
        // Keep stream open until client disconnects
    }

    @Override
    public void getStatus(GetStatusRequest request, StreamObserver<AdapterStatus> observer) {
        // Return SS7 link health, counters
    }
}
```

### Updated Config

```yaml
adapter:
  name: "ss7-vodacom"

# gRPC server (gateway connects here)
grpc:
  bind-address: 0.0.0.0
  bind-port: 9090
  tls: false
  # tls-cert: /etc/ssl/adapter.crt
  # tls-key: /etc/ssl/adapter.key

# SS7 stack (unchanged from previous design)
ss7:
  local-point-code: 1-100-1
  # ... same as before
```

## Updated Project Structure

```
sigtran-adapter/
├── pom.xml
├── src/main/proto/
│   └── smsc/v1/adapter.proto        # Shared protobuf definition
├── src/main/java/
│   └── com/idnteq/sigtran/
│       ├── Main.java
│       ├── config/
│       │   └── AdapterConfig.java
│       ├── grpc/
│       │   ├── SmsAdapterServiceImpl.java   # gRPC service implementation
│       │   └── GrpcServer.java              # Server lifecycle
│       ├── map/
│       │   ├── MapSmsService.java           # MAP SMS orchestration
│       │   ├── SriForSm.java
│       │   ├── MtForwardSm.java
│       │   ├── MoForwardSm.java
│       │   ├── AlertService.java
│       │   └── TpduCodec.java
│       ├── correlation/
│       │   ├── CorrelationStore.java
│       │   └── MemoryCorrelation.java
│       └── ss7/
│           └── StackFactory.java
├── Dockerfile
└── docker-compose.yml
```

## go-smsc Proto Location

```
go-smsc/
├── proto/
│   └── smscv1/
│       ├── adapter.proto            # Source of truth
│       ├── adapter.pb.go            # Generated
│       └── adapter_grpc.pb.go       # Generated
├── gateway/
│   ├── grpc_bind.go                 # gRPC client bind
│   └── grpc_bind_test.go
├── Makefile                         # Add proto generation target
```

## Implementation Phases

### Phase 1: Proto + Gateway gRPC Client
- Define proto file in go-smsc repo
- Generate Go code
- Implement `GRPCBind` client in gateway
- Add `BindType` to pool manager config
- Update admin UI Binds page with type selector
- Test with a mock gRPC server
- **Deliverable:** Gateway can create gRPC binds from admin UI

### Phase 2: Submitter Interface Refactor
- Define `Submitter` interface
- Wrap `smpp.Pool` to implement `Submitter`
- Wrap `GRPCBind` to implement `Submitter`
- Refactor router to use `Submitter` instead of `*smpp.Pool`
- Refactor route table to return `Submitter`
- All existing tests pass (SMPP path unchanged)
- **Deliverable:** Gateway routes to either SMPP or gRPC binds transparently

### Phase 3: Java Adapter Skeleton
- Maven project with corsac-jss7 + gRPC dependencies
- Proto code generation (same .proto file)
- gRPC server with stub implementations
- Config loading from YAML
- `GetStatus` returns hardcoded healthy
- `SubmitMT` returns dummy success
- `StreamDeliveries` keeps stream open
- **Deliverable:** Gateway connects to adapter, submits route through, dummy responses

### Phase 4: SS7 Stack Integration
- corsac-jss7 initialization from config
- M3UA SCTP association establishment
- SCCP SAP registration
- TCAP provider setup
- MAP service activation
- `GetStatus` returns real SS7 link health
- **Deliverable:** Adapter establishes SS7 connectivity

### Phase 5: MT-SMS Flow
- `SubmitMT` → SRI-for-SM → MT-ForwardSM pipeline
- TP-PDU encoding (GSM 03.40)
- Correlation tracking
- Error handling (absent subscriber, network failure)
- **Deliverable:** Send SMS from go-smsc through SS7

### Phase 6: MO-SMS + DLR Flow
- MO-ForwardSM → `StreamDeliveries` push
- ReportSMDeliveryStatus → DLR push
- AlertServiceCentre → alert push + retry trigger
- **Deliverable:** Full bidirectional SMS over SS7

### Phase 7: Production Hardening
- TLS for gRPC
- H2 correlation persistence
- Graceful shutdown (drain MAP dialogues)
- Prometheus metrics export
- Docker image + compose
- **Deliverable:** Production-ready deployment

## Integration from Admin UI

Creating an SS7 bind in the go-smsc admin UI:

**Binds page → Add Bind:**
- Type: `gRPC` (dropdown)
- Name: `ss7-vodacom`
- gRPC Address: `localhost:9090`
- TLS: unchecked

**Routes page → Add MT Route:**
- Prefix: `27*`
- Strategy: failover
- Binds: `ss7-vodacom`

The gateway doesn't know it's SS7 behind the gRPC call. It just submits via the `Submitter` interface and receives deliveries via the streaming channel.

## Monitoring

Both sides expose metrics:

**Gateway (Prometheus):**
- `smscgw_grpc_submit_total{bind="ss7-vodacom", status="success|error"}`
- `smscgw_grpc_submit_latency_seconds{bind="ss7-vodacom"}`
- `smscgw_grpc_delivery_total{bind="ss7-vodacom", type="mo|dlr|alert"}`
- `smscgw_grpc_bind_healthy{bind="ss7-vodacom"}`

**Adapter (Prometheus via JMX exporter):**
- M3UA association state
- SCCP GTT translation counts
- TCAP active dialogues
- MAP operation counts (SRI, MT-Forward, MO-Forward)
- gRPC request counts and latency

## Deployment

```yaml
services:
  smsc-gateway:
    image: ghcr.io/idnteq/go-smsc:latest
    ports:
      - "2776:2776"
      - "8080:8080"

  ss7-adapter-vodacom:
    image: ghcr.io/idnteq/sigtran-adapter:latest
    ports:
      - "9090:9090"     # gRPC (gateway connects here)
      - "8180:8180"     # Management console
    volumes:
      - ./vodacom-adapter.yaml:/app/adapter.yaml
    network_mode: host   # Required for SCTP

  ss7-adapter-mtn:
    image: ghcr.io/idnteq/sigtran-adapter:latest
    ports:
      - "9091:9090"
      - "8181:8180"
    volumes:
      - ./mtn-adapter.yaml:/app/adapter.yaml
    network_mode: host
```
