# SIGTRAN ↔ SMPP Adapter Design

## Context

The go-smsc gateway speaks SMPP on both sides (northbound clients, southbound SMSCs). To connect to mobile networks via SS7/SIGTRAN, we need an adapter that translates between SMPP and MAP/TCAP/SCCP/M3UA. This adapter uses corsac-jss7 (Mobius fork) as the SS7 stack and appears to the go-smsc gateway as a standard SMPP SMSC (a "Bind" in the UI).

## Architecture

```
                        go-smsc Gateway
                             │
                        SMPP (TCP)
                             │
               ┌─────────────┴─────────────┐
               │   SIGTRAN-SMPP Adapter     │
               │   (Java / corsac-jss7)     │
               │                            │
               │  ┌──────────┐ ┌─────────┐  │
               │  │ SMPP     │ │ MAP SMS │  │
               │  │ Server   │ │ Service │  │
               │  └────┬─────┘ └────┬────┘  │
               │       │            │        │
               │  ┌────┴────────────┴────┐   │
               │  │   Correlation Engine │   │
               │  │   (msgID mapping)    │   │
               │  └──────────┬───────────┘   │
               │             │               │
               │  ┌──────────┴───────────┐   │
               │  │  corsac-jss7 Stack   │   │
               │  │  TCAP → SCCP → M3UA  │   │
               │  └──────────┬───────────┘   │
               └─────────────┼───────────────┘
                             │
                      M3UA / SCTP
                             │
                   ┌─────────┴─────────┐
                   │    SS7 Network    │
                   │  (STP, HLR, MSC) │
                   └───────────────────┘
```

The adapter is a standalone Java process. The go-smsc gateway connects to it via SMPP just like any other downstream SMSC. No changes to the Go codebase are needed — you create a "Bind" in the admin UI pointing to the adapter's SMPP port.

## Components

### 1. SMPP Server (Northbound)

Accepts SMPP bind_transceiver from the go-smsc gateway. Uses cloudhopper-smpp or a lightweight SMPP server implementation.

**Handles:**
- `bind_transceiver` → authenticate the gateway
- `submit_sm` → extract source/dest/message, initiate MAP MT-ForwardSM or MO-ForwardSM
- `enquire_link` → keepalive
- `unbind` → graceful disconnect

**Sends back:**
- `submit_sm_resp` → with message ID after MAP operation completes (or after SRI-for-SM + MT-ForwardSM)
- `deliver_sm` → when receiving MAP MO-ForwardSM from the network (mobile-originated SMS)
- `deliver_sm` (DLR) → when receiving MAP ReportSMDeliveryStatus from the network

### 2. MAP SMS Service (Southbound)

Implements the MAP SMS operations over corsac-jss7's TCAP/SCCP/M3UA stack.

**MT-SMS flow (gateway → mobile):**
1. Receive submit_sm from SMPP
2. Extract destination MSISDN
3. Send MAP `SendRoutingInfoForSM` to HLR (via GTT to resolve HLR address)
4. Receive SRI-for-SM response with IMSI + serving MSC address
5. Send MAP `MT-ForwardSM` to the serving MSC
6. Receive MT-ForwardSM response (success/failure)
7. Send submit_sm_resp back via SMPP with message ID
8. Optionally: receive MAP `ReportSMDeliveryStatus` later → send deliver_sm DLR via SMPP

**MO-SMS flow (mobile → gateway):**
1. Receive MAP `MO-ForwardSM` from network (MSC sends to our SMSC address)
2. Extract source MSISDN, destination, message payload
3. Decode TP-layer (GSM 03.40) to extract SMS text
4. Send deliver_sm via SMPP to the gateway
5. Receive deliver_sm_resp from gateway
6. Send MAP MO-ForwardSM response back to MSC

**Alert flow:**
1. HLR sends MAP `AlertServiceCentre` when a previously-unavailable subscriber becomes reachable
2. Trigger retry of queued messages for that subscriber

### 3. Correlation Engine

Maps between SMPP message IDs and MAP dialogue IDs / TCAP transaction IDs.

```
SMPP msg_id: "GW-12345"  ←→  MAP dialogue: 7891
                          ←→  TCAP txn: 0x00A3F1
                          ←→  IMSI: 27821234567
                          ←→  MSC addr: GT +27999000001
```

Stored in a ConcurrentHashMap with TTL-based expiry (configurable, default 24h). For persistence across restarts, optionally backed by an embedded DB (H2 or SQLite) — NOT Cassandra.

### 4. corsac-jss7 Stack Configuration

**M3UA layer:**
- Mode: IPSP (IP Signaling Point) or ASP connecting to an STP
- SCTP association(s) to the network STP or directly to peer nodes
- Local Point Code assignment
- Application Server (AS) and ASP definitions

**SCCP layer:**
- Service Access Points (SAPs) binding SCCP to M3UA
- Remote Signaling Point Codes for HLRs, MSCs
- GTT rules for routing (if acting as own STP) or relay to external STP
- SSN registration: SSN=8 (MSC), SSN=6 (HLR) for sending; SSN=8 for receiving MO-SMS

**TCAP layer:**
- Dialog timeout configuration
- Max concurrent dialogs

**MAP layer:**
- MAP application context for SMS operations
- Version negotiation (MAP v1/v2/v3)

### 5. Configuration

Single YAML configuration file:

```yaml
# Adapter identification
adapter:
  name: "sigtran-adapter-01"
  log-level: INFO

# SMPP server (northbound — go-smsc connects here)
smpp:
  bind-address: 0.0.0.0
  bind-port: 2775
  system-id: "SS7-SMSC"
  password: "secret"
  max-connections: 10
  enquire-link-interval: 30

# SS7 stack configuration
ss7:
  # Local signaling point
  local-point-code: 1-100-1
  network-indicator: international  # international, national, spare

  # SCTP associations to STPs or peer nodes
  associations:
    - name: stp-primary
      host: 10.0.0.1
      port: 2905
      peer-host: 10.0.0.100
      peer-port: 2905

    - name: stp-secondary
      host: 10.0.0.1
      port: 2906
      peer-host: 10.0.0.101
      peer-port: 2905

  # M3UA configuration
  m3ua:
    mode: asp  # asp, sg, ipsp
    application-servers:
      - name: as-stp
        routing-context: 100
        traffic-mode: loadshare
        associations: [stp-primary, stp-secondary]

  # SCCP configuration
  sccp:
    # SSN for our SMSC (we register as SSN 8)
    local-ssn: 8
    protocol-version: ITU

    # Remote signaling points
    remote-spcs:
      - pc: 1-200-1
        name: hlr-main
      - pc: 1-200-2
        name: hlr-backup
      - pc: 1-300-1
        name: msc-region1

    # GTT rules (only needed if NOT using external STP for GTT)
    gtt-rules: []  # Leave empty if STP handles GTT

  # MAP configuration
  map:
    version: 3           # MAP v3 (default), falls back to v2/v1
    dialog-timeout: 30   # seconds
    max-dialogs: 10000
    smsc-gt: "+27999000000"  # Our SMSC's Global Title

# Message handling
messages:
  sri-timeout: 10        # SRI-for-SM timeout (seconds)
  mt-forward-timeout: 30 # MT-ForwardSM timeout (seconds)
  retry-on-absent: true  # Queue messages when subscriber absent
  max-retry-hours: 24    # Max time to retry absent subscriber
  tp-encoding: auto      # auto, gsm7, ucs2

# Correlation store
correlation:
  type: memory           # memory or h2
  ttl-hours: 24
  # h2-path: ./data/correlation  # only if type=h2

# Management
management:
  http-port: 8180        # Web console and REST API
  jmx-port: 9999         # JMX monitoring
```

### 6. STP Mode (Optional)

If you don't have an external STP, the adapter can also function as an STP with GTT by configuring:
- M3UA in SG mode (instead of ASP)
- GTT rules in the SCCP section
- `sccp.canRelay = true`

This makes the adapter a combined STP + MAP SMS node. Useful for small deployments or lab setups.

## Project Structure

```
sigtran-smpp-adapter/
├── pom.xml                          # Maven build (Java 21+)
├── src/main/java/
│   └── com/idnteq/sigtran/
│       ├── Main.java                # Entry point, loads config, starts stack
│       ├── config/
│       │   └── AdapterConfig.java   # YAML config mapping
│       ├── smpp/
│       │   ├── SmppServer.java      # SMPP server accepting gateway connections
│       │   ├── SmppSession.java     # Per-connection session handling
│       │   └── SmppPduHandler.java  # submit_sm → MAP translation trigger
│       ├── map/
│       │   ├── MapSmsService.java   # MAP SMS operation orchestration
│       │   ├── SriForSm.java        # SendRoutingInfoForSM handling
│       │   ├── MtForwardSm.java     # MT-ForwardSM send/receive
│       │   ├── MoForwardSm.java     # MO-ForwardSM receive → SMPP deliver
│       │   ├── AlertService.java    # AlertServiceCentre handling
│       │   └── TpduCodec.java       # GSM 03.40 TP-layer encode/decode
│       ├── correlation/
│       │   ├── CorrelationStore.java    # Interface
│       │   ├── MemoryCorrelation.java   # In-memory with TTL
│       │   └── H2Correlation.java       # H2 embedded DB (optional)
│       └── ss7/
│           └── StackFactory.java    # corsac-jss7 stack initialization
├── src/main/resources/
│   └── adapter.yaml                 # Default configuration
├── src/test/java/                   # Unit tests
├── Dockerfile                       # Multi-stage build
└── docker-compose.yml               # Adapter + go-smsc + mock SS7
```

## Dependencies

```xml
<!-- corsac-jss7 (SS7 stack) -->
<dependency>
    <groupId>com.mobius-software.protocols.ss7</groupId>
    <artifactId>sctp-impl</artifactId>
</dependency>
<dependency>
    <groupId>com.mobius-software.protocols.ss7</groupId>
    <artifactId>m3ua-impl</artifactId>
</dependency>
<dependency>
    <groupId>com.mobius-software.protocols.ss7</groupId>
    <artifactId>sccp-impl</artifactId>
</dependency>
<dependency>
    <groupId>com.mobius-software.protocols.ss7</groupId>
    <artifactId>tcap-impl</artifactId>
</dependency>
<dependency>
    <groupId>com.mobius-software.protocols.ss7</groupId>
    <artifactId>map-impl</artifactId>
</dependency>

<!-- SMPP server -->
<dependency>
    <groupId>com.cloudhopper</groupId>
    <artifactId>ch-smpp</artifactId>
    <version>6.0.0-netty4-beta-3</version>
</dependency>

<!-- Config -->
<dependency>
    <groupId>com.fasterxml.jackson.dataformat</groupId>
    <artifactId>jackson-dataformat-yaml</artifactId>
</dependency>

<!-- Embedded DB (optional) -->
<dependency>
    <groupId>com.h2database</groupId>
    <artifactId>h2</artifactId>
    <optional>true</optional>
</dependency>
```

## Implementation Phases

### Phase 1: Skeleton + SMPP Server
- Maven project setup with corsac-jss7 dependencies
- Config loading from YAML
- SMPP server accepting bind_transceiver
- submit_sm handling (log and respond with dummy msg ID)
- Basic tests with our go-smsc connecting as a client
- **Deliverable:** Adapter shows as a healthy "Bind" in go-smsc admin UI

### Phase 2: SS7 Stack Initialization
- corsac-jss7 SCTP + M3UA + SCCP + TCAP + MAP stack init from config
- ASP mode connection to an STP (or IPSP for direct connection)
- SCCP SAP registration (SSN=8 for SMSC)
- Management web console on HTTP port
- **Deliverable:** Adapter establishes M3UA association with SS7 peer

### Phase 3: MT-SMS (Submit Path)
- submit_sm → SRI-for-SM → MT-ForwardSM pipeline
- TP-PDU encoding (GSM 03.40 SMS-SUBMIT → SMS-DELIVER)
- Correlation tracking (SMPP msg ID ↔ MAP dialogue)
- submit_sm_resp with real message ID after successful MT-ForwardSM
- Error handling: subscriber absent, network failure, timeout
- **Deliverable:** Send SMS from go-smsc through SS7 to a real MSC

### Phase 4: MO-SMS (Receive Path)
- MO-ForwardSM reception from network
- TP-PDU decoding
- deliver_sm generation to go-smsc
- deliver_sm_resp handling
- MO-ForwardSM response back to MSC
- **Deliverable:** Receive SMS from mobile network into go-smsc

### Phase 5: DLR + Alert + Reliability
- ReportSMDeliveryStatus → deliver_sm DLR mapping
- AlertServiceCentre → retry queued messages
- Subscriber-absent queueing with configurable retry
- H2 correlation persistence for crash recovery
- Graceful shutdown (drain in-flight MAP dialogues)
- **Deliverable:** Full production-ready SMS flow with delivery reports

### Phase 6: STP Mode (Optional)
- M3UA SG mode configuration
- GTT rule management via CLI/API
- SCCP relay (`canRelay=true`)
- **Deliverable:** Combined STP + SMSC adapter for small deployments

## Integration with go-smsc

From the go-smsc admin UI:

1. **Binds page:** Create a new bind:
   - Name: `ss7-vodacom`
   - Host: `localhost` (or adapter host)
   - Port: `2775` (adapter's SMPP port)
   - System ID: `gateway`
   - Password: `secret`

2. **Routes page:** Create an MT route:
   - Prefix: `27*`
   - Strategy: failover
   - Binds: `ss7-vodacom`

Now all messages to South African numbers route through the SS7 adapter instead of an SMPP aggregator.

## Testing

### Unit Tests
- TP-PDU encode/decode (GSM 03.40)
- Correlation store TTL expiry
- SMPP ↔ MAP message field mapping
- Config parsing

### Integration Tests
- Full MT-SMS flow with jSS7 simulator (loopback — adapter talks to itself)
- Full MO-SMS flow
- DLR delivery
- Subscriber-absent retry after AlertServiceCentre

### E2E Tests
- go-smsc → adapter → jSS7 simulator → adapter → go-smsc (full round trip)
- Docker Compose setup with all components

## Monitoring

- **JMX:** corsac-jss7 exposes MBeans for all protocol layers
- **Prometheus:** Export jSS7 metrics via JMX exporter
  - M3UA association state
  - SCCP message counts (GTT translations, routing failures)
  - TCAP dialogue counts (active, timeout, error)
  - MAP operation counts (SRI, MT-Forward, MO-Forward, success/failure)
  - SMPP session state, submit/deliver counts
- **Logging:** SLF4J with configurable per-layer verbosity
- **Management console:** corsac-jss7 web UI on configurable HTTP port

## Deployment

```yaml
# docker-compose.yml
services:
  smsc-gateway:
    image: ghcr.io/idnteq/go-smsc:latest
    ports:
      - "2776:2776"    # SMPP northbound
      - "8080:8080"    # Admin UI
    environment:
      GW_SMPP_VERSION: "3.4"

  sigtran-adapter:
    image: ghcr.io/idnteq/sigtran-adapter:latest
    ports:
      - "2775:2775"    # SMPP (gateway connects here)
      - "8180:8180"    # Management console
    volumes:
      - ./adapter.yaml:/app/adapter.yaml
      - ./data:/app/data
    # SCTP requires host networking or specific kernel support
    network_mode: host
```

Note: SCTP in Docker requires either `network_mode: host` or kernel SCTP module loaded with `--cap-add NET_ADMIN`. For production, host networking is recommended for the adapter.
