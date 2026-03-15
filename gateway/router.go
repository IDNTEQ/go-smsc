package gateway

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/idnteq/go-smsc/smpp"
)

// correlation tracks a submitted message for DLR routing.
// The gateway generates its own message ID (gwMsgID) which is returned to
// the engine immediately. When the downstream SMSC responds with its own
// message ID (smscMsgID), we store the mapping so DLRs can be translated.
type correlation struct {
	GwMsgID     string // Gateway-generated message ID (sent to engine)
	NorthConnID string // Which engine submitted this
	MSISDN      string // Destination MSISDN (for affinity)
	SubmittedAt time.Time
}

// Router is the core routing component of the SMSC Gateway.
//
// Submit flow (store-and-forward):
//  1. Engine sends submit_sm
//  2. Gateway immediately ACKs with gateway-generated message ID
//  3. Gateway asynchronously forwards to downstream SMSC
//  4. When SMSC responds, gateway stores smscMsgID→gwMsgID mapping
//
// DLR flow:
//  1. Downstream SMSC sends deliver_sm with DLR containing smscMsgID
//  2. Gateway translates smscMsgID→gwMsgID
//  3. Gateway forwards deliver_sm to engine with gwMsgID in receipt
//  4. Engine gets deliver_sm with the same message ID it received in submit_sm_resp
//
// MO flow:
//  1. Downstream SMSC sends deliver_sm (MO)
//  2. Gateway looks up MSISDN affinity → connID
//  3. Gateway forwards deliver_sm to that engine
//
// Reconnect grace:
//
//	When a target engine is disconnected, the gateway buffers DLR/MO for
//	a configurable grace period (default 60s) to allow the engine to
//	reconnect with the same system_id (preserving all affinity mappings).
//	Only after the grace period expires does it fall back to round-robin.
// forwardTask represents a submit forwarding job for the worker pool.
type forwardTask struct {
	conn       *Connection
	gwMsgID    string
	destAddr   string
	sourceAddr string
	rawBody    []byte
}

type Router struct {
	server          *Server
	southbound      Submitter
	poolManager     *PoolManager
	mtRoutes        *MTRouteTable
	moRoutes        *MORouteTable
	routeConfig     *RouteConfigStore
	msisdnAffinity  *ShardMap[string]       // MSISDN → northbound connID
	smscCorrelation *ShardMap[*correlation]  // downstream smscMsgID → correlation
	gwCorrelation   *ShardMap[*correlation]  // gwMsgID → correlation (for cleanup)
	store           *MessageStore
	metrics         *Metrics
	logger          *zap.Logger
	msgSeq          atomic.Uint64 // for generating unique gateway message IDs
	reconnectGrace  time.Duration // how long to wait for engine reconnect

	// Bounded worker pool for southbound forwarding.
	forwardCh chan forwardTask

	// Rate limiting: max submits per second per connection (0 = unlimited).
	rateLimitTPS int

	// Southbound submit retry config.
	maxSubmitRetries int

	// Drain limits per tick.
	retryDrainLimit       int
	submitRetryDrainLimit int

	// MSISDN blacklist: submits to these numbers are rejected.
	blacklist *ShardMap[struct{}]

	// Atomic activity counters for the admin dashboard (read-side).
	// These are incremented alongside the Prometheus counters which are
	// write-only from the client_golang perspective.
	totalSubmits   atomic.Int64
	totalDLRs      atomic.Int64
	totalMO        atomic.Int64
	totalForwarded atomic.Int64
	totalThrottled atomic.Int64
}

// NewRouter creates a router instance. Server and southbound pool are set
// after construction via SetServer/SetSouthbound to break circular deps.
func NewRouter(store *MessageStore, metrics *Metrics, cfg Config, logger *zap.Logger) *Router {
	queueSize := cfg.ForwardQueueSize
	if queueSize <= 0 {
		queueSize = 10000
	}

	r := &Router{
		msisdnAffinity:        NewShardMap[string](),
		smscCorrelation:       NewShardMap[*correlation](),
		gwCorrelation:         NewShardMap[*correlation](),
		blacklist:             NewShardMap[struct{}](),
		mtRoutes:              NewMTRouteTable(),
		moRoutes:              NewMORouteTable(),
		store:                 store,
		metrics:               metrics,
		logger:                logger,
		reconnectGrace:        time.Duration(cfg.ReconnectGraceSec) * time.Second,
		rateLimitTPS:          cfg.RateLimitTPS,
		maxSubmitRetries:      cfg.MaxSubmitRetries,
		forwardCh:             make(chan forwardTask, queueSize),
		retryDrainLimit:       cfg.RetryDrainLimit,
		submitRetryDrainLimit: cfg.SubmitRetryDrainLimit,
	}
	if r.retryDrainLimit <= 0 {
		r.retryDrainLimit = 200
	}
	if r.submitRetryDrainLimit <= 0 {
		r.submitRetryDrainLimit = 100
	}

	// Load blacklist file if configured.
	if cfg.BlacklistFile != "" {
		count, err := r.LoadBlacklist(cfg.BlacklistFile)
		if err != nil {
			logger.Warn("failed to load blacklist file",
				zap.String("file", cfg.BlacklistFile),
				zap.Error(err),
			)
		} else {
			logger.Info("loaded MSISDN blacklist",
				zap.String("file", cfg.BlacklistFile),
				zap.Int("entries", count),
			)
		}
	}

	return r
}

// LoadBlacklist loads MSISDNs from a file (one per line, # comments allowed).
func (r *Router) LoadBlacklist(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r.blacklist.Set(line, struct{}{})
		count++
	}
	return count, scanner.Err()
}

// SetServer sets the northbound server.
func (r *Router) SetServer(s *Server) {
	r.server = s
}

// SetSouthbound sets the southbound SMPP pool.
// This is the backward-compatible path: when no route table is configured,
// the southbound pool is used as the fallback for all outbound submits.
func (r *Router) SetSouthbound(pool *smpp.Pool) {
	r.southbound = NewSMPPSubmitter(pool)
}

// SetSouthboundSubmitter sets the southbound submitter directly.
// Use this when setting a gRPC bind or a pre-wrapped SMPPSubmitter.
func (r *Router) SetSouthboundSubmitter(s Submitter) {
	r.southbound = s
}

// SetPoolManager sets the multi-pool manager for route-based pool selection.
func (r *Router) SetPoolManager(pm *PoolManager) {
	r.poolManager = pm
}

// SetMTRoutes adds MT routes to the route table.
func (r *Router) SetMTRoutes(routes []*MTRoute) {
	if r.mtRoutes == nil {
		r.mtRoutes = NewMTRouteTable()
	}
	for _, route := range routes {
		r.mtRoutes.AddRoute(route)
	}
}

// SetMORoutes adds MO routes to the route table.
func (r *Router) SetMORoutes(routes []*MORoute) {
	if r.moRoutes == nil {
		r.moRoutes = NewMORouteTable()
	}
	for _, route := range routes {
		r.moRoutes.AddRoute(route)
	}
}

// SetRouteConfig sets the route configuration store.
func (r *Router) SetRouteConfig(rc *RouteConfigStore) {
	r.routeConfig = rc
}

// StartForwardWorkers launches a fixed pool of goroutines that consume from
// the forwarding channel, replacing the previous unbounded per-submit goroutine.
func (r *Router) StartForwardWorkers(n int) {
	if n <= 0 {
		n = 64
	}
	for i := 0; i < n; i++ {
		go r.forwardWorker()
	}
}

func (r *Router) forwardWorker() {
	for task := range r.forwardCh {
		r.forwardSubmitRaw(task.conn, task.gwMsgID, task.destAddr, task.sourceAddr, task.rawBody)
	}
}

// nextMsgID generates a unique gateway message ID.
func (r *Router) nextMsgID() string {
	return fmt.Sprintf("GW-%d", r.msgSeq.Add(1))
}

// HandleSubmit is called when an engine sends submit_sm.
// It checks the blacklist and rate limit, then immediately ACKs the engine
// with a gateway-generated message ID, and asynchronously forwards the raw
// submit_sm body byte-for-byte to the downstream SMSC via SubmitRaw.
//
// The gateway only parses the minimum needed for routing (source/dest addresses)
// and never rebuilds the PDU body — all fields (TON/NPI, ESM class, protocol ID,
// data coding, TLVs, short_message vs message_payload) are preserved unchanged.
func (r *Router) HandleSubmit(connID string, seqNum uint32, body []byte) {
	start := time.Now()

	// Lightweight parse: only extract addresses for routing/affinity.
	sourceAddr, destAddr := ParseSubmitSMAddresses(body)

	c := r.server.GetConnection(connID)

	// Source address enforcement based on client config.
	if c != nil && c.ConnConfig() != nil {
		cfg := c.ConnConfig()
		switch cfg.SourceAddrMode {
		case SourceAddrModeOverride:
			// Always replace source with forced address.
			if cfg.ForceSourceAddr != "" {
				body = RewriteSubmitSMSource(body, cfg.ForceSourceAddr, cfg.ForceSourceTON, cfg.ForceSourceNPI)
				sourceAddr = cfg.ForceSourceAddr
			}
		case SourceAddrModeDefault:
			// Fill in default only when client sends empty source.
			if sourceAddr == "" && cfg.DefaultSourceAddr != "" {
				body = RewriteSubmitSMSource(body, cfg.DefaultSourceAddr, cfg.DefaultSourceTON, cfg.DefaultSourceNPI)
				sourceAddr = cfg.DefaultSourceAddr
			}
		case SourceAddrModeWhitelist:
			// Reject if source not in allowed list.
			if len(cfg.AllowedSourceAddrs) > 0 {
				allowed := false
				for _, a := range cfg.AllowedSourceAddrs {
					if a == sourceAddr {
						allowed = true
						break
					}
				}
				if !allowed {
					r.logger.Debug("submit blocked by source address whitelist",
						zap.String("conn_id", connID),
						zap.String("source", sourceAddr),
					)
					if r.store != nil {
						_ = r.store.LogMessage(&MessageLogEntry{
							GwMsgID:    fmt.Sprintf("rejected-%d", r.msgSeq.Add(1)),
							ConnID:     connID,
							SourceAddr: sourceAddr,
							DestAddr:   destAddr,
							Status:     "rejected",
						})
					}
					if c != nil {
						_ = c.SendSubmitSMResp(seqNum, smpp.StatusSubmitFail, "")
					}
					return
				}
			}
		}
		// SourceAddrModePassthrough (default): do nothing, forward as-is.
	}

	// Check MSISDN blacklist.
	if destAddr != "" {
		if _, blocked := r.blacklist.Get(destAddr); blocked {
			r.logger.Debug("submit blocked by blacklist",
				zap.String("conn_id", connID),
				zap.String("dest", destAddr),
			)
			r.metrics.BlacklistedTotal.Inc()
			if r.store != nil {
				_ = r.store.LogMessage(&MessageLogEntry{
					GwMsgID:    fmt.Sprintf("rejected-%d", r.msgSeq.Add(1)),
					ConnID:     connID,
					SourceAddr: sourceAddr,
					DestAddr:   destAddr,
					Status:     "rejected",
				})
			}
			if c != nil {
				_ = c.SendSubmitSMResp(seqNum, smpp.StatusSubmitFail, "")
			}
			return
		}
	}

	// Per-connection prefix filtering.
	// Strip leading '+' from destination for matching — MSISDNs may use
	// E.164 format (+27821234567) while prefixes are digits-only (27).
	if c != nil && c.ConnConfig() != nil && len(c.ConnConfig().AllowedPrefixes) > 0 {
		normalizedDest := strings.TrimPrefix(destAddr, "+")
		allowed := false
		for _, prefix := range c.ConnConfig().AllowedPrefixes {
			if strings.HasPrefix(normalizedDest, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			r.logger.Debug("submit blocked by prefix filter",
				zap.String("conn_id", connID),
				zap.String("dest", destAddr),
			)
			if r.store != nil {
				_ = r.store.LogMessage(&MessageLogEntry{
					GwMsgID:    fmt.Sprintf("rejected-%d", r.msgSeq.Add(1)),
					ConnID:     connID,
					SourceAddr: sourceAddr,
					DestAddr:   destAddr,
					Status:     "rejected",
				})
			}
			if c != nil {
				_ = c.SendSubmitSMResp(seqNum, smpp.StatusSubmitFail, "")
			}
			return
		}
	}

	// Check per-connection rate limit. Use per-connection MaxTPS if set,
	// otherwise fall back to global rateLimitTPS.
	effectiveTPS := r.rateLimitTPS
	if c != nil && c.ConnConfig() != nil && c.ConnConfig().MaxTPS > 0 {
		effectiveTPS = c.ConnConfig().MaxTPS
	}
	if c != nil && effectiveTPS > 0 {
		if c.CurrentTPS() >= uint64(effectiveTPS) {
			r.logger.Debug("submit throttled",
				zap.String("conn_id", connID),
				zap.Uint64("current_tps", c.CurrentTPS()),
				zap.Int("limit", effectiveTPS),
			)
			r.metrics.ThrottledTotal.Inc()
			r.totalThrottled.Add(1)
			if r.store != nil {
				_ = r.store.LogMessage(&MessageLogEntry{
					GwMsgID:    fmt.Sprintf("rejected-%d", r.msgSeq.Add(1)),
					ConnID:     connID,
					SourceAddr: sourceAddr,
					DestAddr:   destAddr,
					Status:     "rejected",
				})
			}
			_ = c.SendSubmitSMResp(seqNum, smpp.StatusThrottled, "")
			return
		}
		c.RecordSubmit()
	}

	// Update MSISDN → connID affinity.
	if destAddr != "" {
		r.msisdnAffinity.Set(destAddr, connID)
	}

	// Generate gateway message ID.
	gwMsgID := r.nextMsgID()

	// Immediately ACK the engine — store-and-forward pattern.
	if c != nil {
		_ = c.SendSubmitSMResp(seqNum, smpp.StatusOK, gwMsgID)
	}

	r.metrics.SubmitTotal.WithLabelValues("accepted").Inc()
	r.totalSubmits.Add(1)
	r.metrics.SubmitLatency.Observe(time.Since(start).Seconds())

	// Store the correlation with gateway message ID.
	corr := &correlation{
		GwMsgID:     gwMsgID,
		NorthConnID: connID,
		MSISDN:      destAddr,
		SubmittedAt: time.Now(),
	}
	r.gwCorrelation.Set(gwMsgID, corr)

	// Persist submit record for crash recovery (keyed by gw:{gwMsgID}
	// since we don't know the downstream SMSC message ID yet).
	if r.store != nil {
		_ = r.store.StoreSubmit(&SubmitRecord{
			GwMsgID:     gwMsgID,
			NorthConnID: connID,
			OrigSeqNum:  seqNum,
			MSISDN:      destAddr,
			SourceAddr:  sourceAddr,
			Payload:     body, // raw submit_sm body for replay
			SubmittedAt: time.Now(),
		})
	}

	// Log message as accepted.
	if r.store != nil {
		_ = r.store.LogMessage(&MessageLogEntry{
			GwMsgID:    gwMsgID,
			ConnID:     connID,
			SourceAddr: sourceAddr,
			DestAddr:   destAddr,
			Status:     "accepted",
		})
	}

	// Enqueue to bounded worker pool for southbound forwarding.
	// The *Connection pointer is captured now so in-flight decrement targets
	// the correct connection even if the engine reconnects.
	if c != nil {
		c.InFlightAdd(1)
	}
	select {
	case r.forwardCh <- forwardTask{conn: c, gwMsgID: gwMsgID, destAddr: destAddr, sourceAddr: sourceAddr, rawBody: body}:
		// Enqueued successfully.
	default:
		// Queue full — enqueue to Pebble submit-retry so the message is
		// eventually forwarded (or generates a synthetic failure DLR if
		// retries are exhausted). The engine already received submit_sm_resp
		// so we must not silently drop the work.
		r.logger.Warn("forward queue full, enqueueing submit retry",
			zap.String("gw_msg_id", gwMsgID),
		)
		r.metrics.ThrottledTotal.Inc()
		r.totalThrottled.Add(1)
		if c != nil {
			c.InFlightAdd(-1)
		}
		r.enqueueSubmitRetryOrFail(gwMsgID, connID, destAddr, sourceAddr, body, 0)
	}
}

// forwardSubmitRaw sends the raw submit_sm body byte-for-byte to the downstream
// SMSC and stores the smscMsgID→gwMsgID mapping for DLR translation.
// No PDU parsing or rebuilding — the body passes through unchanged.
//
// The *Connection pointer is captured by the caller before the goroutine starts
// so that in-flight decrement always targets the correct connection even if
// the engine reconnects (which replaces the connection under the same ID).
func (r *Router) forwardSubmitRaw(c *Connection, gwMsgID, destAddr, sourceAddr string, rawBody []byte) {
	// Decrement in-flight counter on the captured connection when done.
	defer func() {
		if c != nil {
			c.InFlightAdd(-1)
		}
	}()

	connID := ""
	if c != nil {
		connID = c.ID
	}

	// Try route table first, fall back to single pool.
	var sub Submitter
	if r.poolManager != nil && r.mtRoutes != nil {
		s, _, resolveErr := r.mtRoutes.Resolve(destAddr, r.poolManager)
		if resolveErr == nil && s != nil {
			sub = s
			r.metrics.RouteResolutions.WithLabelValues("mt", "routed").Inc()
		}
	}
	if sub == nil && r.southbound != nil {
		sub = r.southbound
		r.metrics.RouteResolutions.WithLabelValues("mt", "fallback").Inc()
	}
	if sub == nil {
		r.logger.Error("no southbound pool available",
			zap.String("gw_msg_id", gwMsgID),
			zap.String("dest", destAddr),
		)
		r.metrics.RouteResolutions.WithLabelValues("mt", "no_route").Inc()
		r.metrics.SubmitTotal.WithLabelValues("forward_error").Inc()
		r.enqueueSubmitRetryOrFail(gwMsgID, connID, destAddr, sourceAddr, rawBody, 0)
		return
	}

	resp, err := sub.SubmitRaw(rawBody)
	if err != nil {
		r.logger.Warn("southbound submit failed",
			zap.String("conn_id", connID),
			zap.String("gw_msg_id", gwMsgID),
			zap.String("dest", destAddr),
			zap.Error(err),
		)
		r.metrics.SubmitTotal.WithLabelValues("forward_error").Inc()
		r.enqueueSubmitRetryOrFail(gwMsgID, connID, destAddr, sourceAddr, rawBody, 0)
		return
	}

	if resp.Error != nil {
		r.logger.Warn("southbound submit rejected",
			zap.String("conn_id", connID),
			zap.String("gw_msg_id", gwMsgID),
			zap.String("dest", destAddr),
			zap.Error(resp.Error),
		)
		r.metrics.SubmitTotal.WithLabelValues("forward_rejected").Inc()
		r.enqueueSubmitRetryOrFail(gwMsgID, connID, destAddr, sourceAddr, rawBody, 0)
		return
	}

	// Store smscMsgID → correlation for DLR routing.
	corr, ok := r.gwCorrelation.Get(gwMsgID)
	if !ok {
		corr = &correlation{
			GwMsgID:     gwMsgID,
			NorthConnID: connID,
			MSISDN:      destAddr,
			SubmittedAt: time.Now(),
		}
	}
	r.smscCorrelation.Set(resp.MessageID, corr)

	// Store the definitive Pebble record keyed by msg:{smscMsgID} with both
	// the gateway and downstream SMSC message IDs. Then clean up the initial
	// gw:{gwMsgID} key that was written before we knew the SMSC ID.
	if r.store != nil {
		_ = r.store.StoreSubmit(&SubmitRecord{
			GwMsgID:     gwMsgID,
			SmscMsgID:   resp.MessageID,
			NorthConnID: connID,
			OrigSeqNum:  0,
			MSISDN:      destAddr,
			SourceAddr:  sourceAddr,
			Payload:     rawBody,
			SubmittedAt: corr.SubmittedAt,
		})
		_ = r.store.DeleteSubmitByGwID(gwMsgID)

		// Update durable status for REST query.
		if connID == "rest-api" {
			_ = r.store.SetMessageStatus(&MessageStatus{
				GwMsgID:   gwMsgID,
				To:        destAddr,
				From:      sourceAddr,
				Status:    "forwarded",
				SmscMsgID: resp.MessageID,
				UpdatedAt: time.Now(),
			})
		}
	}

	// Update message log to forwarded.
	if r.store != nil {
		_ = r.store.UpdateMessageLog(gwMsgID, func(e *MessageLogEntry) {
			e.Status = "forwarded"
			e.SmscMsgID = resp.MessageID
		})
	}

	r.metrics.SubmitTotal.WithLabelValues("forwarded").Inc()
	r.totalForwarded.Add(1)

	r.logger.Debug("submit forwarded",
		zap.String("gw_msg_id", gwMsgID),
		zap.String("smsc_msg_id", resp.MessageID),
		zap.String("dest", destAddr),
	)
}

// HandleDeliver is the DeliverHandler for the southbound SMPP pool.
// Called when the downstream SMSC sends deliver_sm (DLR or MO).
// Returns nil to ACK the downstream SMSC immediately.
func (r *Router) HandleDeliver(sourceAddr, destAddr string, esmClass byte, payload []byte) error {
	start := time.Now()

	if smpp.IsDLR(esmClass) {
		r.handleDLR(sourceAddr, destAddr, esmClass, payload, start)
	} else {
		r.handleMO(sourceAddr, destAddr, esmClass, payload, start)
	}

	// Always return nil to ACK the downstream SMSC immediately.
	// If we can't deliver to the engine right now, we buffer it in Pebble.
	return nil
}

// handleDLR routes a DLR deliver_sm to the engine that submitted the message.
// It translates the SMSC message ID to the gateway message ID that the
// engine knows about.
func (r *Router) handleDLR(sourceAddr, destAddr string, esmClass byte, payload []byte, start time.Time) {
	r.totalDLRs.Add(1)

	// Parse DLR receipt to extract the downstream SMSC's message_id.
	receipt := smpp.ParseDLRReceipt(string(payload))
	if receipt == nil {
		r.logger.Warn("unparseable DLR receipt", zap.String("payload", string(payload)))
		r.metrics.DLRTotal.WithLabelValues("unknown", "false").Inc()
		return
	}

	// Lookup correlation: smscMsgID → {gwMsgID, connID}.
	corr, ok := r.smscCorrelation.Get(receipt.MessageID)
	if !ok {
		// Try Pebble store as fallback (crash recovery path).
		if r.store != nil {
			if record, _ := r.store.GetSubmit(receipt.MessageID); record != nil {
				corr = &correlation{
					GwMsgID:     record.GwMsgID,
					NorthConnID: record.NorthConnID,
					MSISDN:      record.MSISDN,
				}
				ok = true
			}
		}
	}

	if !ok {
		r.logger.Debug("DLR for unknown message_id",
			zap.String("smsc_msg_id", receipt.MessageID),
		)
		r.metrics.DLRTotal.WithLabelValues(receipt.Status, "false").Inc()
		return
	}

	// Translate the message ID in the DLR receipt from SMSC ID → gateway ID.
	// The engine expects the same message ID it received in submit_sm_resp.
	translatedPayload := translateDLRMessageID(string(payload), receipt.MessageID, corr.GwMsgID)
	body := BuildDeliverSMBody(sourceAddr, destAddr, esmClass, []byte(translatedPayload))

	connID := corr.NorthConnID

	// Update message log with DLR status.
	if r.store != nil {
		_ = r.store.UpdateMessageLog(corr.GwMsgID, func(e *MessageLogEntry) {
			e.Status = "delivered"
			e.DLRStatus = receipt.Status
			if receipt.Status != "DELIVRD" {
				e.Status = "failed"
			}
		})
	}

	// REST-originated submissions: there is no SMPP connection to deliver to.
	// The DLR callback IS the terminal delivery path.
	if connID == "rest-api" {
		r.deliverDLRCallback(corr.GwMsgID, receipt.Status, destAddr, sourceAddr)

		// Update durable status for REST query.
		if r.store != nil {
			_ = r.store.SetMessageStatus(&MessageStatus{
				GwMsgID:   corr.GwMsgID,
				To:        corr.MSISDN,
				Status:    "delivered",
				DLRStatus: receipt.Status,
				UpdatedAt: time.Now(),
			})
		}

		// Clean up correlation.
		r.smscCorrelation.Delete(receipt.MessageID)
		r.gwCorrelation.Delete(corr.GwMsgID)
		if r.store != nil {
			_ = r.store.DeleteSubmit(receipt.MessageID)
		}
		r.metrics.DLRTotal.WithLabelValues(receipt.Status, "true").Inc()
		r.metrics.DeliverLatency.Observe(time.Since(start).Seconds())
		return
	}

	// SMPP-originated submissions: deliver DLR back to engine via SMPP.
	delivered := r.deliverWithGrace(connID, body, corr.MSISDN, esmClass, sourceAddr, destAddr, corr.GwMsgID, true)

	if delivered {
		// Clean up correlation after DLR delivered.
		r.smscCorrelation.Delete(receipt.MessageID)
		r.gwCorrelation.Delete(corr.GwMsgID)
		if r.store != nil {
			_ = r.store.DeleteSubmit(receipt.MessageID)
		}
		r.metrics.DLRTotal.WithLabelValues(receipt.Status, "true").Inc()
		r.metrics.DeliverLatency.Observe(time.Since(start).Seconds())

		// Also deliver DLR via REST callback if registered (SMPP submit
		// with callback_url set via a future hybrid path).
		r.deliverDLRCallback(corr.GwMsgID, receipt.Status, destAddr, sourceAddr)
	} else {
		r.metrics.DLRTotal.WithLabelValues(receipt.Status, "buffered").Inc()
	}
}

// handleMO routes an MO deliver_sm to the engine based on MO route table
// or MSISDN affinity fallback.
func (r *Router) handleMO(sourceAddr, destAddr string, esmClass byte, payload []byte, start time.Time) {
	r.totalMO.Add(1)

	// Check MO route table first.
	if r.moRoutes != nil {
		target, _ := r.moRoutes.Resolve(sourceAddr, destAddr)
		if target != nil {
			r.metrics.RouteResolutions.WithLabelValues("mo", "routed").Inc()
			if target.Type == "http" {
				r.deliverMOCallback(target.CallbackURL, sourceAddr, destAddr, payload)
				r.metrics.MOTotal.WithLabelValues("http_route").Inc()
				r.metrics.DeliverLatency.Observe(time.Since(start).Seconds())
				return
			}
			if target.Type == "smpp" && target.ConnID != "" {
				// Route to specific SMPP connection.
				body := BuildDeliverSMBody(sourceAddr, destAddr, esmClass, payload)
				delivered := r.deliverWithGrace(target.ConnID, body, sourceAddr, esmClass, sourceAddr, destAddr, fmt.Sprintf("mo-%d", time.Now().UnixNano()), false)
				if delivered {
					r.metrics.MOTotal.WithLabelValues("smpp_route").Inc()
					r.metrics.DeliverLatency.Observe(time.Since(start).Seconds())
				} else {
					r.metrics.MOTotal.WithLabelValues("buffered").Inc()
				}
				return
			}
		}
	}

	// Existing MSISDN affinity fallback.
	r.metrics.RouteResolutions.WithLabelValues("mo", "fallback").Inc()
	connID, ok := r.msisdnAffinity.Get(sourceAddr)
	if !ok {
		// No affinity — round-robin to any connected engine.
		connID = r.server.RoundRobinConnID()
		if connID == "" {
			r.logger.Warn("no engines connected for MO",
				zap.String("source", sourceAddr),
			)
			if r.store != nil {
				_ = r.store.EnqueueRetry(fmt.Sprintf("mo-%d", time.Now().UnixNano()), &PendingDeliver{
					MSISDN:     sourceAddr,
					PDUBody:    BuildDeliverSMBody(sourceAddr, destAddr, esmClass, payload),
					ESMClass:   esmClass,
					SourceAddr: sourceAddr,
					DestAddr:   destAddr,
					EnqueuedAt: time.Now(),
				})
			}
			r.metrics.MOTotal.WithLabelValues("buffered").Inc()
			return
		}
	}

	body := BuildDeliverSMBody(sourceAddr, destAddr, esmClass, payload)
	delivered := r.deliverWithGrace(connID, body, sourceAddr, esmClass, sourceAddr, destAddr, fmt.Sprintf("mo-%d", time.Now().UnixNano()), false)

	if delivered {
		r.metrics.MOTotal.WithLabelValues("true").Inc()
		r.metrics.DeliverLatency.Observe(time.Since(start).Seconds())
	} else {
		r.metrics.MOTotal.WithLabelValues("buffered").Inc()
	}
}

// deliverWithGrace attempts to deliver a PDU to a specific engine connection.
// If the engine is disconnected but disconnected recently (within reconnect
// grace period), the message is buffered for retry — giving the engine time
// to reconnect with the same system_id and preserve affinity mappings.
//
// For DLRs (isDLR=true): NEVER falls back to round-robin. DLRs must always
// go back to the engine that submitted the message. If that engine is
// disconnected, the DLR is buffered until the engine reconnects or the
// retry TTL expires.
//
// For MO (isDLR=false): Falls back to round-robin after the grace period
// since MO routing is best-effort based on MSISDN affinity.
//
// Returns true if delivery succeeded (engine ACKed the deliver_sm).
func (r *Router) deliverWithGrace(connID string, body []byte, msisdn string, esmClass byte, sourceAddr, destAddr, retryID string, isDLR bool) bool {
	// Try direct delivery first.
	_, err := r.server.DeliverToConn(connID, body)
	if err == nil {
		return true
	}

	// Engine disconnected. Check if it disconnected recently.
	disconnectTime, wasConnected := r.server.DisconnectedAt(connID)
	withinGrace := wasConnected && time.Since(disconnectTime) < r.reconnectGrace

	if withinGrace {
		// Buffer for retry — engine may reconnect soon with same system_id.
		r.logger.Debug("engine disconnected within grace period, buffering",
			zap.String("conn_id", connID),
			zap.Duration("since_disconnect", time.Since(disconnectTime)),
			zap.Duration("grace_period", r.reconnectGrace),
		)
		if r.store != nil {
			_ = r.store.EnqueueRetry(retryID, &PendingDeliver{
				TargetConnID: connID,
				MSISDN:       msisdn,
				PDUBody:      body,
				ESMClass:     esmClass,
				SourceAddr:   sourceAddr,
				DestAddr:     destAddr,
				EnqueuedAt:   time.Now(),
			})
		}
		return false
	}

	// Grace period expired or engine was never connected.
	// DLRs: always buffer — never round-robin to a different engine.
	// MO: try round-robin fallback.
	if !isDLR {
		fallbackID := r.server.RoundRobinConnID()
		if fallbackID != "" {
			_, err = r.server.DeliverToConn(fallbackID, body)
			if err == nil {
				r.logger.Debug("MO delivered via round-robin fallback",
					zap.String("original_conn", connID),
					zap.String("fallback_conn", fallbackID),
				)
				return true
			}
		}
	}

	// Buffer for retry.
	if isDLR {
		r.logger.Debug("DLR buffered — target engine disconnected, no round-robin",
			zap.String("conn_id", connID),
		)
	} else {
		r.logger.Debug("no engines available, buffering",
			zap.String("conn_id", connID),
		)
	}
	if r.store != nil {
		_ = r.store.EnqueueRetry(retryID, &PendingDeliver{
			TargetConnID: connID,
			MSISDN:       msisdn,
			PDUBody:      body,
			ESMClass:     esmClass,
			SourceAddr:   sourceAddr,
			DestAddr:     destAddr,
			EnqueuedAt:   time.Now(),
		})
	}
	return false
}

// translateDLRMessageID replaces the SMSC message ID in a DLR receipt text
// with the gateway message ID so the engine sees the ID it received in
// submit_sm_resp.
//
// DLR format: "id:SMSC-123 sub:001 dlvrd:001 ... stat:DELIVRD ..."
// → becomes: "id:GW-456 sub:001 dlvrd:001 ... stat:DELIVRD ..."
func translateDLRMessageID(receiptText, smscMsgID, gwMsgID string) string {
	return strings.Replace(receiptText, "id:"+smscMsgID, "id:"+gwMsgID, 1)
}

// RunRetryLoop periodically drains buffered DLR/MO and attempts redelivery.
func (r *Router) RunRetryLoop(ctx context.Context, interval, maxAge time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.drainRetries(maxAge)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Router) drainRetries(maxAge time.Duration) {
	if r.store == nil {
		return
	}

	pending, err := r.store.DrainRetries(maxAge, r.retryDrainLimit)
	if err != nil {
		r.logger.Warn("drain retries error", zap.Error(err))
		return
	}

	for _, p := range pending {
		isDLR := smpp.IsDLR(p.ESMClass)
		connID := p.TargetConnID

		if connID == "" || r.server.GetConnection(connID) == nil {
			if isDLR {
				// DLRs must go back to the original engine — never round-robin.
				_ = r.store.EnqueueRetry(fmt.Sprintf("retry-%d", time.Now().UnixNano()), p)
				continue
			}
			// MO: try round-robin fallback.
			connID = r.server.RoundRobinConnID()
		}
		if connID == "" {
			// No engines — re-enqueue.
			_ = r.store.EnqueueRetry(fmt.Sprintf("retry-%d", time.Now().UnixNano()), p)
			continue
		}

		if _, err := r.server.DeliverToConn(connID, p.PDUBody); err != nil {
			_ = r.store.EnqueueRetry(fmt.Sprintf("retry-%d", time.Now().UnixNano()), p)
		}
	}
}

// RunMetricsUpdater periodically updates gauge metrics.
func (r *Router) RunMetricsUpdater(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.metrics.AffinityTableSize.Set(float64(r.msisdnAffinity.Len()))
			r.metrics.CorrelationTableSize.Set(float64(r.smscCorrelation.Len()))
			if r.store != nil {
				r.metrics.StoreMessages.Set(float64(r.store.MessageCount()))
				r.metrics.RetryQueueSize.Set(float64(r.store.PendingRetryCount()))
			}
			if r.poolManager != nil {
				for _, h := range r.poolManager.AllHealth() {
					val := 0.0
					if h.Healthy {
						val = 1.0
					}
					r.metrics.PoolHealthGauge.WithLabelValues(h.Name).Set(val)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// CleanupCorrelations removes expired correlations from in-memory maps.
// Uses two-phase collect-then-delete to avoid deadlock: Range() holds a
// read lock on each shard, and calling Delete() inside the callback would
// try to acquire a write lock on the same shard → deadlock.
func (r *Router) CleanupCorrelations(ctx context.Context, maxAge time.Duration) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-maxAge)

			// Phase 1: collect expired keys.
			var smscExpired []string
			r.smscCorrelation.Range(func(key string, val *correlation) bool {
				if val.SubmittedAt.Before(cutoff) {
					smscExpired = append(smscExpired, key)
				}
				return true
			})
			// Phase 2: delete outside of Range iteration.
			for _, key := range smscExpired {
				r.smscCorrelation.Delete(key)
			}

			var gwExpired []string
			r.gwCorrelation.Range(func(key string, val *correlation) bool {
				if val.SubmittedAt.Before(cutoff) {
					gwExpired = append(gwExpired, key)
				}
				return true
			})
			for _, key := range gwExpired {
				r.gwCorrelation.Delete(key)
			}
		case <-ctx.Done():
			return
		}
	}
}

// AffinitySize returns the number of MSISDN affinity entries.
func (r *Router) AffinitySize() int { return r.msisdnAffinity.Len() }

// CorrelationSize returns the number of pending SMSC correlations.
func (r *Router) CorrelationSize() int { return r.smscCorrelation.Len() }

// SubmitRetryCount returns the number of pending submit retries from the store.
func (r *Router) SubmitRetryCount() int {
	if r.store != nil {
		return r.store.PendingSubmitRetryCount()
	}
	return 0
}

// TotalSubmits returns the total number of accepted submits since start.
func (r *Router) TotalSubmits() int64 { return r.totalSubmits.Load() }

// TotalDLRs returns the total number of DLRs received since start.
func (r *Router) TotalDLRs() int64 { return r.totalDLRs.Load() }

// TotalMO returns the total number of MO messages received since start.
func (r *Router) TotalMO() int64 { return r.totalMO.Load() }

// TotalForwarded returns the total number of successfully forwarded submits since start.
func (r *Router) TotalForwarded() int64 { return r.totalForwarded.Load() }

// TotalThrottled returns the total number of throttled submits since start.
func (r *Router) TotalThrottled() int64 { return r.totalThrottled.Load() }

// enqueueSubmitRetryOrFail either queues a failed submit for retry, or sends
// a synthetic failure DLR if retries are exhausted. This ensures the engine
// always learns about delivery failures — without this, cards would stay
// stuck in awaiting_dlr forever.
func (r *Router) enqueueSubmitRetryOrFail(gwMsgID, connID, destAddr, sourceAddr string, rawBody []byte, retryCount int) {
	if r.maxSubmitRetries > 0 && retryCount < r.maxSubmitRetries && r.store != nil {
		_ = r.store.EnqueueSubmitRetry(&PendingSubmit{
			GwMsgID:    gwMsgID,
			ConnID:     connID,
			MSISDN:     destAddr,
			SourceAddr: sourceAddr,
			RawBody:    rawBody,
			RetryCount: retryCount + 1,
			EnqueuedAt: time.Now(),
		})
		r.logger.Debug("submit queued for retry",
			zap.String("gw_msg_id", gwMsgID),
			zap.Int("retry", retryCount+1),
			zap.Int("max", r.maxSubmitRetries),
		)
		return
	}

	// Retries exhausted — send synthetic failure DLR to the engine.
	r.sendSyntheticDLR(connID, gwMsgID, destAddr, sourceAddr)
}

// sendSyntheticDLR generates a failure DLR deliver_sm back to the engine so
// the card transitions out of awaiting_dlr. The DLR receipt uses the gateway
// message ID that the engine received in submit_sm_resp.
func (r *Router) sendSyntheticDLR(connID, gwMsgID, destAddr, sourceAddr string) {
	receipt := fmt.Sprintf("id:%s sub:001 dlvrd:000 submit date:0000000000 done date:0000000000 stat:UNDELIV err:001 text:", gwMsgID)

	r.metrics.SyntheticDLRTotal.Inc()

	// Update message log to failed.
	if r.store != nil {
		_ = r.store.UpdateMessageLog(gwMsgID, func(e *MessageLogEntry) {
			e.Status = "failed"
			e.DLRStatus = "UNDELIV"
		})
	}

	// REST-originated: update durable status, fire callback, no SMPP delivery.
	if connID == "rest-api" {
		if r.store != nil {
			_ = r.store.SetMessageStatus(&MessageStatus{
				GwMsgID:   gwMsgID,
				To:        destAddr,
				From:      sourceAddr,
				Status:    "failed",
				DLRStatus: "UNDELIV",
				UpdatedAt: time.Now(),
			})
		}
		r.deliverDLRCallback(gwMsgID, "UNDELIV", destAddr, sourceAddr)
		r.gwCorrelation.Delete(gwMsgID)
		r.logger.Info("synthetic failure DLR for REST submission",
			zap.String("gw_msg_id", gwMsgID),
			zap.String("dest", destAddr),
		)
		return
	}

	body := BuildDeliverSMBody(sourceAddr, destAddr, 0x04, []byte(receipt)) // 0x04 = DLR esm_class

	delivered := r.deliverWithGrace(connID, body, destAddr, 0x04, sourceAddr, destAddr, fmt.Sprintf("synth-%s", gwMsgID), true)

	if delivered {
		r.logger.Info("synthetic failure DLR sent",
			zap.String("gw_msg_id", gwMsgID),
			zap.String("conn_id", connID),
			zap.String("dest", destAddr),
		)
	} else {
		r.logger.Warn("synthetic failure DLR buffered (engine unavailable)",
			zap.String("gw_msg_id", gwMsgID),
			zap.String("conn_id", connID),
		)
	}

	// Clean up correlation — this message is done.
	r.gwCorrelation.Delete(gwMsgID)
}

// RunSubmitRetryLoop periodically drains failed southbound submits and retries
// them. After max retries, generates a synthetic failure DLR.
func (r *Router) RunSubmitRetryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.drainSubmitRetries()
		case <-ctx.Done():
			return
		}
	}
}

func (r *Router) drainSubmitRetries() {
	if r.store == nil {
		return
	}

	pending, err := r.store.DrainSubmitRetries(r.submitRetryDrainLimit)
	if err != nil {
		r.logger.Warn("drain submit retries error", zap.Error(err))
		return
	}

	for _, p := range pending {
		r.metrics.SubmitRetryTotal.Inc()

		// Try route table first, fall back to single pool.
		var sub Submitter
		if r.poolManager != nil && r.mtRoutes != nil {
			rs, _, resolveErr := r.mtRoutes.Resolve(p.MSISDN, r.poolManager)
			if resolveErr == nil && rs != nil {
				sub = rs
			}
		}
		if sub == nil && r.southbound != nil {
			sub = r.southbound
		}
		if sub == nil {
			r.enqueueSubmitRetryOrFail(p.GwMsgID, p.ConnID, p.MSISDN, p.SourceAddr, p.RawBody, p.RetryCount)
			continue
		}

		resp, err := sub.SubmitRaw(p.RawBody)
		if err != nil || resp.Error != nil {
			// Still failing — retry or give up.
			r.enqueueSubmitRetryOrFail(p.GwMsgID, p.ConnID, p.MSISDN, p.SourceAddr, p.RawBody, p.RetryCount)
			continue
		}

		// Success — store the correlation for DLR routing.
		corr := &correlation{
			GwMsgID:     p.GwMsgID,
			NorthConnID: p.ConnID,
			MSISDN:      p.MSISDN,
			SubmittedAt: time.Now(),
		}
		r.smscCorrelation.Set(resp.MessageID, corr)

		// Persist to Pebble for crash recovery (mirrors forwardSubmitRaw).
		if r.store != nil {
			_ = r.store.StoreSubmit(&SubmitRecord{
				GwMsgID:     p.GwMsgID,
				SmscMsgID:   resp.MessageID,
				NorthConnID: p.ConnID,
				MSISDN:      p.MSISDN,
				SourceAddr:  p.SourceAddr,
				Payload:     p.RawBody,
				SubmittedAt: corr.SubmittedAt,
			})
			_ = r.store.DeleteSubmitByGwID(p.GwMsgID)

			// Update durable status for REST query.
			if p.ConnID == "rest-api" {
				_ = r.store.SetMessageStatus(&MessageStatus{
					GwMsgID:   p.GwMsgID,
					To:        p.MSISDN,
					From:      p.SourceAddr,
					Status:    "forwarded",
					SmscMsgID: resp.MessageID,
					UpdatedAt: time.Now(),
				})
			}
		}

		// Update message log to forwarded.
		if r.store != nil {
			_ = r.store.UpdateMessageLog(p.GwMsgID, func(e *MessageLogEntry) {
				e.Status = "forwarded"
				e.SmscMsgID = resp.MessageID
			})
		}

		r.metrics.SubmitTotal.WithLabelValues("forwarded").Inc()
		r.totalForwarded.Add(1)

		r.logger.Debug("submit retry succeeded",
			zap.String("gw_msg_id", p.GwMsgID),
			zap.String("smsc_msg_id", resp.MessageID),
			zap.Int("retry", p.RetryCount),
		)
	}
}
