package gateway

import "github.com/idnteq/go-smsc/smpp"

// SMPPSubmitter wraps an smpp.Pool to implement the Submitter interface.
type SMPPSubmitter struct {
	pool *smpp.Pool
}

// NewSMPPSubmitter creates a Submitter backed by an SMPP connection pool.
func NewSMPPSubmitter(pool *smpp.Pool) *SMPPSubmitter {
	return &SMPPSubmitter{pool: pool}
}

// SubmitRaw forwards the raw submit_sm body byte-for-byte to the SMPP pool.
func (s *SMPPSubmitter) SubmitRaw(body []byte) (*smpp.SubmitResponse, error) {
	return s.pool.SubmitRaw(body)
}

// ActiveConnections returns the number of bound SMPP connections.
func (s *SMPPSubmitter) ActiveConnections() int {
	return s.pool.ActiveConnections()
}

// IsHealthy returns true if the pool has at least one active connection.
func (s *SMPPSubmitter) IsHealthy() bool {
	return s.pool.ActiveConnections() > 0
}

// Close shuts down the underlying SMPP pool.
func (s *SMPPSubmitter) Close() error {
	return s.pool.Close()
}

// Pool returns the underlying smpp.Pool for backward compatibility.
func (s *SMPPSubmitter) Pool() *smpp.Pool {
	return s.pool
}
