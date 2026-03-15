# gRPC SMS Adapter Protocol

The `smscv1/adapter.proto` file defines the gRPC interface between the go-smsc gateway and downstream SMS adapters (SS7/SIGTRAN, HTTP, or custom delivery backends).

The gateway acts as a **gRPC client**. Adapters implement the **gRPC server**.

## Service Overview

| RPC | Direction | Description |
|-----|-----------|-------------|
| `SubmitMT` | Gateway → Adapter | Submit an MT-SMS for delivery |
| `StreamDeliveries` | Adapter → Gateway | Server-streaming channel for MO-SMS, DLRs, and alerts |
| `GetStatus` | Gateway → Adapter | Query adapter health and link status |
| `CancelMT` | Gateway → Adapter | Cancel a queued MT-SMS |

## Generating Client/Server Code

### Prerequisites

Install the Protocol Buffer compiler (`protoc`) from [github.com/protocolbuffers/protobuf/releases](https://github.com/protocolbuffers/protobuf/releases).

### Go

```bash
# Install plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Generate (already done — output is in this directory)
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/smscv1/adapter.proto
```

Generated files: `adapter.pb.go`, `adapter_grpc.pb.go`

### Java / Kotlin

```xml
<!-- pom.xml — add protobuf-maven-plugin -->
<plugin>
    <groupId>org.xolstice.maven.plugins</groupId>
    <artifactId>protobuf-maven-plugin</artifactId>
    <version>0.6.1</version>
    <configuration>
        <protocArtifact>com.google.protobuf:protoc:3.25.5:exe:${os.detected.classifier}</protocArtifact>
        <pluginId>grpc-java</pluginId>
        <pluginArtifact>io.grpc:protoc-gen-grpc-java:1.68.0:exe:${os.detected.classifier}</pluginArtifact>
        <protoSourceRoot>${project.basedir}/src/main/proto</protoSourceRoot>
    </configuration>
    <executions>
        <execution>
            <goals>
                <goal>compile</goal>
                <goal>compile-custom</goal>
            </goals>
        </execution>
    </executions>
</plugin>

<!-- Dependencies -->
<dependency>
    <groupId>io.grpc</groupId>
    <artifactId>grpc-netty-shaded</artifactId>
    <version>1.68.0</version>
</dependency>
<dependency>
    <groupId>io.grpc</groupId>
    <artifactId>grpc-protobuf</artifactId>
    <version>1.68.0</version>
</dependency>
<dependency>
    <groupId>io.grpc</groupId>
    <artifactId>grpc-stub</artifactId>
    <version>1.68.0</version>
</dependency>
```

Copy `adapter.proto` to `src/main/proto/smsc/v1/adapter.proto` and run `mvn compile`. Generates `SMSAdapterServiceGrpc` with the server base class and client stubs.

For **Gradle**:
```kotlin
// build.gradle.kts
plugins {
    id("com.google.protobuf") version "0.9.4"
}

protobuf {
    protoc { artifact = "com.google.protobuf:protoc:3.25.5" }
    plugins {
        create("grpc") { artifact = "io.grpc:protoc-gen-grpc-java:1.68.0" }
    }
    generateProtoTasks {
        all().forEach { it.plugins { create("grpc") } }
    }
}
```

### Python

```bash
pip install grpcio grpcio-tools

python -m grpc_tools.protoc \
    -I. \
    --python_out=. \
    --grpc_python_out=. \
    proto/smscv1/adapter.proto
```

Generates `adapter_pb2.py` and `adapter_pb2_grpc.py`. Implement the server:

```python
import grpc
from smsc.v1 import adapter_pb2, adapter_pb2_grpc

class SMSAdapterServicer(adapter_pb2_grpc.SMSAdapterServiceServicer):
    def SubmitMT(self, request, context):
        # Deliver the SMS via your backend
        return adapter_pb2.SubmitMTResponse(
            message_id="msg-123",
            status=0,
        )

    def StreamDeliveries(self, request, context):
        # Yield MO/DLR messages as they arrive
        while True:
            yield adapter_pb2.DeliveryMessage(...)

    def GetStatus(self, request, context):
        return adapter_pb2.AdapterStatus(healthy=True)
```

### Rust

```toml
# Cargo.toml
[dependencies]
tonic = "0.12"
prost = "0.13"
tokio = { version = "1", features = ["full"] }

[build-dependencies]
tonic-build = "0.12"
```

```rust
// build.rs
fn main() {
    tonic_build::compile_protos("proto/smscv1/adapter.proto").unwrap();
}
```

Generates Rust types and a `SmsAdapterService` trait to implement.

### C# / .NET

```bash
dotnet add package Grpc.Net.Client
dotnet add package Google.Protobuf
dotnet add package Grpc.Tools
```

Add to `.csproj`:
```xml
<ItemGroup>
    <Protobuf Include="proto/smscv1/adapter.proto" GrpcServices="Both" />
</ItemGroup>
```

### TypeScript / Node.js

```bash
npm install @grpc/grpc-js @grpc/proto-loader
# or for generated code:
npm install grpc_tools_node_protoc_ts
```

With `@grpc/proto-loader` (dynamic, no codegen):
```typescript
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

const pkg = protoLoader.loadSync('proto/smscv1/adapter.proto');
const proto = grpc.loadPackageDefinition(pkg).smsc.v1;
```

### C / C++

```bash
protoc --cpp_out=. --grpc_out=. \
       --plugin=protoc-gen-grpc=$(which grpc_cpp_plugin) \
       proto/smscv1/adapter.proto
```

Generates `adapter.pb.h`, `adapter.pb.cc`, `adapter.grpc.pb.h`, `adapter.grpc.pb.cc`.

## Implementing an Adapter

An adapter is a gRPC server that implements `SMSAdapterService`. The go-smsc gateway connects to it as a gRPC client (configured as a "Bind" with type gRPC in the admin UI).

Minimal adapter flow:

1. Start a gRPC server on a configured port
2. Implement `SubmitMT` — receive an SMS submit request, deliver it via your backend (SS7, HTTP, carrier API, etc.), return the result
3. Implement `StreamDeliveries` — when your backend receives an incoming SMS (MO) or delivery report (DLR), push it to the gateway via the open stream
4. Implement `GetStatus` — return health information about your backend connections

The gateway handles all SMPP client management, routing, correlation, retry logic, and admin UI. The adapter only needs to handle the actual message delivery to/from the network.

## Testing with grpcurl

```bash
# Install grpcurl
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# List services (adapter must have reflection enabled)
grpcurl -plaintext localhost:9090 list

# Check adapter health
grpcurl -plaintext localhost:9090 smsc.v1.SMSAdapterService/GetStatus

# Submit a test message
grpcurl -plaintext -d '{
  "message_id": "test-1",
  "source_addr": "ACME",
  "source_addr_ton": 5,
  "dest_addr": "+27821234567",
  "dest_addr_ton": 1,
  "dest_addr_npi": 1,
  "payload": "SGVsbG8gV29ybGQ=",
  "data_coding": 0,
  "register_dlr": true
}' localhost:9090 smsc.v1.SMSAdapterService/SubmitMT
```
