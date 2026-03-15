package gateway

import "github.com/idnteq/go-smsc/smpp"

// Submitter is the interface for downstream message delivery.
// Both SMPP pools and gRPC adapters implement this.
type Submitter interface {
	// SubmitRaw sends a raw SMPP submit_sm body (for SMPP pools).
	// gRPC adapters parse the raw body into structured fields.
	SubmitRaw(body []byte) (*smpp.SubmitResponse, error)

	// ActiveConnections returns the number of active downstream connections.
	ActiveConnections() int

	// IsHealthy returns whether the submitter can accept messages.
	IsHealthy() bool

	// Close shuts down the submitter.
	Close() error

	// BindType returns "smpp" or "grpc".
	BindType() string
}
