package gateway

import "github.com/idnteq/go-smsc/smpp"

// Submitter is the interface for downstream message delivery.
type Submitter interface {
	// SubmitRaw sends a raw SMPP submit_sm body.
	SubmitRaw(body []byte) (*smpp.SubmitResponse, error)

	// ActiveConnections returns the number of active downstream connections.
	ActiveConnections() int

	// IsHealthy returns whether the submitter can accept messages.
	IsHealthy() bool

	// Close shuts down the submitter.
	Close() error

	// BindType returns "smpp".
	BindType() string
}
