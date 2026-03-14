package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/idnteq/go-smsc/smpp"
)

// Server is the northbound SMPP server that accepts connections from
// OTA Engine instances. Each engine connects via standard SMPP
// bind_transceiver and submits messages that are forwarded southbound.
//
// Connection IDs are derived from the engine's SMPP system_id so that
// reconnecting engines get the same ID, preserving MSISDN→connID affinity.
type Server struct {
	cfg      Config
	listener net.Listener

	connMu      sync.RWMutex
	conns       map[string]*Connection // connID (=system_id) → Connection
	disconnects map[string]time.Time   // connID → time of last disconnect

	router  *Router
	metrics *Metrics
	logger  *zap.Logger
	done    chan struct{}
}

// NewServer creates a new northbound SMPP server.
func NewServer(cfg Config, metrics *Metrics, logger *zap.Logger) *Server {
	return &Server{
		cfg:         cfg,
		conns:       make(map[string]*Connection),
		disconnects: make(map[string]time.Time),
		metrics:     metrics,
		logger:      logger,
		done:        make(chan struct{}),
	}
}

// SetRouter sets the router after construction (breaks circular dependency).
func (s *Server) SetRouter(r *Router) {
	s.router = r
}

// Start begins listening for engine SMPP connections.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.ListenAddr, err)
	}

	tlsCfg, err := LoadTLSConfig(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("TLS config: %w", err)
	}
	if tlsCfg != nil {
		s.listener = tls.NewListener(listener, tlsCfg)
		s.logger.Info("TLS enabled on northbound listener")
	} else {
		s.listener = listener
	}
	s.logger.Info("SMSC Gateway northbound listening", zap.String("addr", s.cfg.ListenAddr))

	go s.acceptLoop()
	return nil
}

// Stop gracefully shuts down the server. It waits up to DrainTimeoutSec
// for in-flight operations to complete before closing connections.
func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		_ = s.listener.Close()
	}

	// Graceful drain: wait for in-flight operations to complete.
	drainTimeout := time.Duration(s.cfg.DrainTimeoutSec) * time.Second
	if drainTimeout > 0 {
		deadline := time.After(drainTimeout)
		s.logger.Info("draining in-flight operations", zap.Duration("timeout", drainTimeout))
	drainLoop:
		for {
			total := s.totalInFlight()
			if total == 0 {
				s.logger.Info("drain complete, no in-flight operations")
				break
			}
			select {
			case <-deadline:
				s.logger.Warn("drain timeout reached, closing with in-flight operations",
					zap.Int32("in_flight", total),
				)
				break drainLoop
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	s.connMu.Lock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = make(map[string]*Connection)
	s.connMu.Unlock()
	s.logger.Info("SMSC Gateway northbound stopped")
}

// totalInFlight returns the sum of in-flight operations across all connections.
func (s *Server) totalInFlight() int32 {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	var total int32
	for _, c := range s.conns {
		total += c.InFlightCount()
	}
	return total
}

func (s *Server) acceptLoop() {
	seq := 0
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.logger.Error("accept error", zap.Error(err))
			continue
		}

		// Use a temporary ID until we know the system_id from bind.
		seq++
		tempID := fmt.Sprintf("pending-%d", seq)
		c := NewConnection(tempID, conn, s.logger)

		s.logger.Debug("TCP connection accepted",
			zap.String("remote", conn.RemoteAddr().String()),
		)

		go s.handleConnection(c)
	}
}

func (s *Server) handleConnection(c *Connection) {
	defer func() {
		if c.IsBound() {
			s.connMu.Lock()
			// Only remove if this is still the active connection for this ID
			if existing, ok := s.conns[c.ID]; ok && existing == c {
				delete(s.conns, c.ID)
				s.disconnects[c.ID] = time.Now()
			}
			s.connMu.Unlock()
			s.metrics.NorthboundConnections.Set(float64(s.connectionCount()))
			s.logger.Info("engine disconnected", zap.String("conn_id", c.ID))
		}
		_ = c.Close()
	}()

	c.ReadLoop(
		s.done,
		BindConfig{
			Password:       s.cfg.ServerPassword,
			AllowedEngines: s.cfg.AllowedEngines,
			ServerSystemID: s.cfg.ServerSystemID,
			SMPPVersion:    s.cfg.SMPPVersion,
		},
		s.onSubmit,
		s.onDeliverResp,
		s.onBind,
	)
}

// onBind is called when a connection completes bind_transceiver.
// It uses the system_id as the stable connection ID.
func (s *Server) onBind(c *Connection, systemID string) {
	// Use system_id as the connection ID for stable affinity.
	c.ID = systemID
	c.logger = c.logger.With(zap.String("conn_id", systemID))

	s.connMu.Lock()
	// Close any existing connection with the same system_id.
	if old, ok := s.conns[systemID]; ok {
		s.logger.Info("replacing existing connection",
			zap.String("conn_id", systemID),
		)
		_ = old.Close()
	}
	s.conns[systemID] = c
	delete(s.disconnects, systemID) // Clear disconnect timestamp
	s.connMu.Unlock()

	s.metrics.NorthboundConnections.Set(float64(s.connectionCount()))
	s.logger.Info("engine bound",
		zap.String("conn_id", systemID),
		zap.String("remote", c.conn.RemoteAddr().String()),
	)
}

// onSubmit handles submit_sm from an engine.
func (s *Server) onSubmit(connID string, seqNum uint32, body []byte) {
	if s.router == nil {
		s.logger.Error("no router configured")
		return
	}
	s.router.HandleSubmit(connID, seqNum, body)
}

// onDeliverResp handles deliver_sm_resp from an engine.
func (s *Server) onDeliverResp(connID string, seqNum uint32, status uint32) {
	s.connMu.RLock()
	c, ok := s.conns[connID]
	s.connMu.RUnlock()
	if ok {
		c.ResolveDeliverResp(seqNum, status)
	}
}

// GetConnection returns a connection by ID, or nil if not found/bound.
func (s *Server) GetConnection(connID string) *Connection {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	c := s.conns[connID]
	if c != nil && c.IsBound() {
		return c
	}
	return nil
}

// DisconnectedAt returns when a connection was last disconnected, and
// whether the disconnect is recent enough to still wait for reconnection.
func (s *Server) DisconnectedAt(connID string) (time.Time, bool) {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	t, ok := s.disconnects[connID]
	return t, ok
}

// DeliverToConn sends a deliver_sm PDU to a specific engine connection and
// waits for the engine's deliver_sm_resp (ACK). This ensures at-least-once
// delivery: the caller knows whether the engine actually processed the PDU,
// not just whether the TCP write succeeded.
//
// Returns the sequence number used, or an error if the connection is gone,
// the write fails, or the engine NACKs/doesn't respond within 10 seconds.
func (s *Server) DeliverToConn(connID string, body []byte) (uint32, error) {
	s.connMu.RLock()
	c, ok := s.conns[connID]
	s.connMu.RUnlock()
	if !ok || !c.IsBound() {
		return 0, fmt.Errorf("connection %s not found", connID)
	}

	// Allocate sequence number and register wait BEFORE sending to avoid
	// a race where the response arrives before we start listening.
	seqNum := c.seqNum.Add(1)
	ackCh := c.RegisterDeliverWait(seqNum)

	pdu := &smpp.PDU{
		CommandID:      smpp.CmdDeliverSM,
		CommandStatus:  smpp.StatusOK,
		SequenceNumber: seqNum,
		Body:           body,
	}
	if err := c.WritePDU(pdu); err != nil {
		c.pendingDelivers.Delete(seqNum)
		return 0, err
	}

	// Wait for engine to ACK the deliver_sm.
	select {
	case status := <-ackCh:
		if status == smpp.StatusOK {
			return seqNum, nil
		}
		return seqNum, fmt.Errorf("deliver_sm NACKed: %s", FormatError(status))
	case <-time.After(10 * time.Second):
		c.pendingDelivers.Delete(seqNum)
		return seqNum, fmt.Errorf("deliver_sm_resp timeout")
	}
}

// RoundRobinConnID returns a connID from the active bound connections.
// Returns "" if no connections are available.
func (s *Server) RoundRobinConnID() string {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	for id, c := range s.conns {
		if c.IsBound() {
			return id
		}
	}
	return ""
}

// BoundConnectionIDs returns a list of all currently bound connection IDs.
func (s *Server) BoundConnectionIDs() []string {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	ids := make([]string, 0, len(s.conns))
	for id, c := range s.conns {
		if c.IsBound() {
			ids = append(ids, id)
		}
	}
	return ids
}

// RunEnquireLink sends periodic enquire_link PDUs to all bound connections
// to keep the SMPP sessions alive. Called as a goroutine.
func (s *Server) RunEnquireLink(ctx context.Context) {
	interval := time.Duration(s.cfg.EnquireLinkSec) * time.Second
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.connMu.RLock()
			conns := make([]*Connection, 0, len(s.conns))
			for _, c := range s.conns {
				if c.IsBound() {
					conns = append(conns, c)
				}
			}
			s.connMu.RUnlock()

			for _, c := range conns {
				seq := c.seqNum.Add(1)
				pdu := &smpp.PDU{
					CommandID:      smpp.CmdEnquireLink,
					CommandStatus:  smpp.StatusOK,
					SequenceNumber: seq,
				}
				if err := c.WritePDU(pdu); err != nil {
					s.logger.Debug("enquire_link failed",
						zap.String("conn_id", c.ID),
						zap.Error(err),
					)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// RunStaleChecker periodically checks for idle connections and closes them.
// A connection is stale if no PDU has been received for IdleTimeoutSec.
func (s *Server) RunStaleChecker(ctx context.Context) {
	timeout := time.Duration(s.cfg.IdleTimeoutSec) * time.Second
	if timeout <= 0 {
		return
	}

	// Check every half the idle timeout.
	ticker := time.NewTicker(timeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.connMu.RLock()
			var stale []*Connection
			for _, c := range s.conns {
				if c.IsBound() && now.Sub(c.LastActivity()) > timeout {
					stale = append(stale, c)
				}
			}
			s.connMu.RUnlock()

			for _, c := range stale {
				s.logger.Info("closing stale connection",
					zap.String("conn_id", c.ID),
					zap.Duration("idle", now.Sub(c.LastActivity())),
				)
				_ = c.Close()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) connectionCount() int {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	count := 0
	for _, c := range s.conns {
		if c.IsBound() {
			count++
		}
	}
	return count
}

// ConnectionCount returns the number of active bound connections (public).
func (s *Server) ConnectionCount() int {
	return s.connectionCount()
}

// ConnectionInfo describes a northbound engine connection for the admin API.
type ConnectionInfo struct {
	ID         string    `json:"id"`
	SystemID   string    `json:"system_id"`
	RemoteAddr string    `json:"remote_addr"`
	BoundSince time.Time `json:"bound_since"`
	InFlight   int32     `json:"in_flight"`
}

// ListConnections returns details of all bound connections.
func (s *Server) ListConnections() []ConnectionInfo {
	s.connMu.RLock()
	defer s.connMu.RUnlock()

	result := make([]ConnectionInfo, 0, len(s.conns))
	for _, c := range s.conns {
		if !c.IsBound() {
			continue
		}
		result = append(result, ConnectionInfo{
			ID:         c.ID,
			SystemID:   c.SystemID,
			RemoteAddr: c.conn.RemoteAddr().String(),
			BoundSince: c.createdAt,
			InFlight:   c.InFlightCount(),
		})
	}
	return result
}
