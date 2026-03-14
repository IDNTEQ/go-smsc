package smpp

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// Config holds the SMPP transceiver connection parameters.
type Config struct {
	Host           string
	Port           int
	SystemID       string
	Password       string
	SystemType     string
	SourceAddr     string
	SourceAddrTON  byte
	SourceAddrNPI  byte
	EnquireLinkSec int

	// TLS
	TLSEnabled            bool
	TLSInsecureSkipVerify bool

	// Bind
	BindMode         BindMode // default: BindTransceiver (zero value)
	InterfaceVersion byte     // default: 0x34 (set in NewClientWithWorkers if 0)
}

// SubmitRequest represents an SMS to submit via SMPP.
type SubmitRequest struct {
	MSISDN      string
	DestTON     byte
	DestNPI     byte
	ESMClass    byte
	ProtocolID  byte
	DataCoding  byte
	Payload     []byte // binary payload (UDH + 03.48 packet)
	RegisterDLR bool
}

// SubmitResponse contains the SMSC's response to a submit_sm.
type SubmitResponse struct {
	MessageID string
	Error     error
}

// DeliverHandler is called when a deliver_sm PDU is received (DLR or MO).
// Returning nil causes an ACK (deliver_sm_resp with StatusOK).
// Returning an error causes a NACK (deliver_sm_resp with StatusSysErr),
// which prompts the SMSC to retry delivery.
type DeliverHandler func(sourceAddr string, destAddr string, esmClass byte, payload []byte) error

// QuerySMResponse contains the SMSC's response to a query_sm.
type QuerySMResponse struct {
	MessageID    string
	FinalDate    string
	MessageState byte
	ErrorCode    byte
}

// Client manages an SMPP transceiver connection over raw TCP.
type Client struct {
	config             Config
	handler            DeliverHandler
	deliverWorkers     int
	deliverQueueSize   int
	negotiatedVersion  byte
	logger             *zap.Logger
	mu                 sync.Mutex
	conn               net.Conn
	bound              bool
	seqNum             uint32
	pending            map[uint32]chan *PDU
	pendingMu          sync.Mutex
	done               chan struct{}
	deliverQ           chan deliverMessage

	deliverQueueDepth     metric.Int64ObservableGauge
	deliverBackpressure   metric.Int64Counter
	deliverQueueDrops     metric.Int64Counter
	deliverQueueDepthReg  metric.Registration
}

type deliverMessage struct {
	sourceAddr string
	destAddr   string
	esmClass   byte
	payload    []byte
	seqNum     uint32 // original PDU sequence for deferred deliver_sm_resp
}

// NewClient creates a new SMPP transceiver client.
func NewClient(config Config, handler DeliverHandler, logger *zap.Logger) *Client {
	return NewClientWithWorkers(config, handler, 8, 25000, logger)
}

// NewClientWithWorkers creates a new SMPP transceiver client with configurable
// deliver handler workers and queue size.
func NewClientWithWorkers(config Config, handler DeliverHandler, deliverWorkers, deliverQueueSize int, logger *zap.Logger) *Client {
	if config.EnquireLinkSec <= 0 {
		config.EnquireLinkSec = 30
	}
	if config.InterfaceVersion == 0 {
		config.InterfaceVersion = 0x34
	}
	if deliverWorkers <= 0 {
		deliverWorkers = 8
	}
	if deliverQueueSize <= 0 {
		deliverQueueSize = 25000
	}
	c := &Client{
		config:           config,
		handler:          handler,
		deliverWorkers:   deliverWorkers,
		deliverQueueSize: deliverQueueSize,
		logger:           logger,
		pending:          make(map[uint32]chan *PDU),
		done:             make(chan struct{}),
		deliverQ:         make(chan deliverMessage, deliverQueueSize),
	}

	meter := otel.Meter("smpp-client")
	c.deliverBackpressure, _ = meter.Int64Counter("smpp.deliver_backpressure",
		metric.WithDescription("Times deliver queue enqueue was delayed by backpressure"))
	c.deliverQueueDrops, _ = meter.Int64Counter("smpp.deliver_queue_drops",
		metric.WithDescription("Deliver messages dropped after backpressure timeout"))
	c.deliverQueueDepth, _ = meter.Int64ObservableGauge("smpp.deliver_queue_depth",
		metric.WithDescription("Current deliver queue depth"))
	if c.deliverQueueDepth != nil {
		c.deliverQueueDepthReg, _ = meter.RegisterCallback(
			func(_ context.Context, o metric.Observer) error {
				o.ObserveInt64(c.deliverQueueDepth, int64(len(c.deliverQ)))
				return nil
			}, c.deliverQueueDepth)
	}

	return c
}

// nextSeq returns the next sequence number (1-based, wrapping).
func (c *Client) nextSeq() uint32 {
	return atomic.AddUint32(&c.seqNum, 1)
}

// Connect establishes the TCP connection and performs the SMPP bind_transceiver handshake.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.bound {
		return fmt.Errorf("already bound")
	}

	addr := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	c.logger.Info("connecting to SMSC", zap.String("addr", addr))

	dialer := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if c.config.TLSEnabled {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: c.config.TLSInsecureSkipVerify,
			ServerName:         c.config.Host,
		}
		conn, err = tls.DialWithDialer(&dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial SMSC at %s: %w", addr, err)
	}
	c.conn = conn
	c.done = make(chan struct{})
	c.deliverQ = make(chan deliverMessage, c.deliverQueueSize)

	// Start the reader goroutine before sending bind so we can receive the response.
	c.startDeliverLoop()
	go c.ReadLoop()

	// Send bind PDU (transceiver, transmitter, or receiver).
	seq := c.nextSeq()
	cmdID := bindCommandID(c.config.BindMode)
	bindBody := EncodeBind(c.config.BindMode, c.config.SystemID, c.config.Password, c.config.SystemType, c.config.InterfaceVersion)
	bindPDU := &PDU{
		CommandID:      cmdID,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           bindBody,
	}

	respCh := c.registerPending(seq)
	cmdName := CommandName(cmdID)

	if err := c.writePDU(bindPDU); err != nil {
		c.unregisterPending(seq)
		_ = conn.Close()
		return fmt.Errorf("send %s: %w", cmdName, err)
	}

	// Wait for bind response.
	respCmdID := bindRespCommandID(c.config.BindMode)
	select {
	case resp := <-respCh:
		if resp.CommandStatus != StatusOK {
			_ = conn.Close()
			return fmt.Errorf("%s failed with status 0x%08X", cmdName, resp.CommandStatus)
		}
		// Parse bind response to extract system_id and TLVs.
		smscID, tlvs := ParseBindResp(respCmdID, resp.Body)
		c.negotiatedVersion = c.config.InterfaceVersion
		if tlvs != nil {
			if ver, ok := tlvs.GetUint8(TagSCInterfaceVersion); ok {
				c.negotiatedVersion = ver
			}
		}
		c.bound = true
		c.logger.Info("SMPP bind successful",
			zap.String("system_id", c.config.SystemID),
			zap.String("smsc_system_id", smscID),
			zap.String("bind_mode", cmdName),
			zap.Uint8("negotiated_version", c.negotiatedVersion),
		)
	case <-time.After(15 * time.Second):
		_ = conn.Close()
		return fmt.Errorf("%s response timeout", cmdName)
	case <-ctx.Done():
		_ = conn.Close()
		return ctx.Err()
	}

	// Start enquire_link keepalive loop.
	go c.enquireLinkLoop()

	return nil
}

// Submit sends an SMS via SMPP submit_sm and waits for the response.
func (c *Client) Submit(req *SubmitRequest) (*SubmitResponse, error) {
	c.mu.Lock()
	if !c.bound {
		c.mu.Unlock()
		return nil, fmt.Errorf("not bound to SMSC")
	}
	c.mu.Unlock()

	var registeredDelivery byte
	if req.RegisterDLR {
		registeredDelivery = 0x01
	}

	body := EncodeSubmitSM(
		c.config.SourceAddr,
		c.config.SourceAddrTON,
		c.config.SourceAddrNPI,
		req.MSISDN,
		req.DestTON,
		req.DestNPI,
		req.ESMClass,
		req.ProtocolID,
		req.DataCoding,
		registeredDelivery,
		req.Payload,
	)

	seq := c.nextSeq()
	pdu := &PDU{
		CommandID:      CmdSubmitSM,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           body,
	}

	respCh := c.registerPending(seq)

	if err := c.writePDU(pdu); err != nil {
		c.unregisterPending(seq)
		return nil, fmt.Errorf("send submit_sm: %w", err)
	}

	// Wait for submit_sm_resp.
	select {
	case resp := <-respCh:
		if resp.CommandStatus != StatusOK {
			return &SubmitResponse{
				Error: fmt.Errorf("submit_sm failed with status 0x%08X", resp.CommandStatus),
			}, nil
		}
		msgID := ParseSubmitSMResp(resp.Body)
		c.logger.Debug("submit_sm_resp received",
			zap.String("message_id", msgID),
			zap.String("msisdn", req.MSISDN),
		)
		return &SubmitResponse{MessageID: msgID}, nil
	case <-time.After(30 * time.Second):
		c.unregisterPending(seq)
		return nil, fmt.Errorf("submit_sm response timeout")
	}
}

// SubmitRaw sends a pre-built submit_sm body as-is and waits for the response.
// Unlike Submit, this does not encode the body — it forwards the raw PDU body
// byte-for-byte. Used by the SMSC Gateway to transparently proxy submit_sm
// without parsing/rebuilding the PDU.
func (c *Client) SubmitRaw(body []byte) (*SubmitResponse, error) {
	c.mu.Lock()
	if !c.bound {
		c.mu.Unlock()
		return nil, fmt.Errorf("not bound to SMSC")
	}
	c.mu.Unlock()

	seq := c.nextSeq()
	pdu := &PDU{
		CommandID:      CmdSubmitSM,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           body,
	}

	respCh := c.registerPending(seq)

	if err := c.writePDU(pdu); err != nil {
		c.unregisterPending(seq)
		return nil, fmt.Errorf("send submit_sm (raw): %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.CommandStatus != StatusOK {
			return &SubmitResponse{
				Error: fmt.Errorf("submit_sm failed with status 0x%08X", resp.CommandStatus),
			}, nil
		}
		msgID := ParseSubmitSMResp(resp.Body)
		return &SubmitResponse{MessageID: msgID}, nil
	case <-time.After(30 * time.Second):
		c.unregisterPending(seq)
		return nil, fmt.Errorf("submit_sm response timeout")
	}
}

// NegotiatedVersion returns the SMPP interface version negotiated during bind.
// Returns 0 if the client has not yet bound.
func (c *Client) NegotiatedVersion() byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negotiatedVersion
}

// QuerySM sends a query_sm PDU and waits for the response.
func (c *Client) QuerySM(messageID, sourceAddr string, sourceTON, sourceNPI byte) (*QuerySMResponse, error) {
	c.mu.Lock()
	if !c.bound {
		c.mu.Unlock()
		return nil, fmt.Errorf("not bound to SMSC")
	}
	c.mu.Unlock()

	body := EncodeQuerySM(messageID, sourceAddr, sourceTON, sourceNPI)
	seq := c.nextSeq()
	pdu := &PDU{
		CommandID:      CmdQuerySM,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           body,
	}

	respCh := c.registerPending(seq)

	if err := c.writePDU(pdu); err != nil {
		c.unregisterPending(seq)
		return nil, fmt.Errorf("send query_sm: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.CommandStatus != StatusOK {
			return nil, fmt.Errorf("query_sm failed with status 0x%08X", resp.CommandStatus)
		}
		msgID, finalDate, msgState, errCode := ParseQuerySMResp(resp.Body)
		return &QuerySMResponse{
			MessageID:    msgID,
			FinalDate:    finalDate,
			MessageState: msgState,
			ErrorCode:    errCode,
		}, nil
	case <-time.After(30 * time.Second):
		c.unregisterPending(seq)
		return nil, fmt.Errorf("query_sm response timeout")
	}
}

// CancelSM sends a cancel_sm PDU and waits for the response.
func (c *Client) CancelSM(serviceType, messageID, sourceAddr string, sourceTON, sourceNPI byte, destAddr string, destTON, destNPI byte) error {
	c.mu.Lock()
	if !c.bound {
		c.mu.Unlock()
		return fmt.Errorf("not bound to SMSC")
	}
	c.mu.Unlock()

	body := EncodeCancelSM(serviceType, messageID, sourceAddr, sourceTON, sourceNPI, destAddr, destTON, destNPI)
	seq := c.nextSeq()
	pdu := &PDU{
		CommandID:      CmdCancelSM,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           body,
	}

	respCh := c.registerPending(seq)

	if err := c.writePDU(pdu); err != nil {
		c.unregisterPending(seq)
		return fmt.Errorf("send cancel_sm: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.CommandStatus != StatusOK {
			return fmt.Errorf("cancel_sm failed with status 0x%08X", resp.CommandStatus)
		}
		return nil
	case <-time.After(30 * time.Second):
		c.unregisterPending(seq)
		return fmt.Errorf("cancel_sm response timeout")
	}
}

// ReplaceSM sends a replace_sm PDU and waits for the response.
func (c *Client) ReplaceSM(messageID, sourceAddr string, sourceTON, sourceNPI byte,
	scheduleDeliveryTime, validityPeriod string,
	registeredDelivery, dataCoding byte, shortMessage []byte) error {

	c.mu.Lock()
	if !c.bound {
		c.mu.Unlock()
		return fmt.Errorf("not bound to SMSC")
	}
	c.mu.Unlock()

	smLength := byte(len(shortMessage))
	body := EncodeReplaceSM(messageID, sourceAddr, sourceTON, sourceNPI,
		scheduleDeliveryTime, validityPeriod,
		registeredDelivery, dataCoding, smLength, shortMessage)
	seq := c.nextSeq()
	pdu := &PDU{
		CommandID:      CmdReplaceSM,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           body,
	}

	respCh := c.registerPending(seq)

	if err := c.writePDU(pdu); err != nil {
		c.unregisterPending(seq)
		return fmt.Errorf("send replace_sm: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.CommandStatus != StatusOK {
			return fmt.Errorf("replace_sm failed with status 0x%08X", resp.CommandStatus)
		}
		return nil
	case <-time.After(30 * time.Second):
		c.unregisterPending(seq)
		return fmt.Errorf("replace_sm response timeout")
	}
}

// Close unbinds from the SMSC and closes the TCP connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.bound {
		return nil
	}

	c.logger.Info("unbinding from SMSC")

	seq := c.nextSeq()
	unbindPDU := &PDU{
		CommandID:      CmdUnbind,
		CommandStatus:  StatusOK,
		SequenceNumber: seq,
		Body:           nil,
	}
	// Best-effort unbind; don't wait for response.
	_ = c.writePDU(unbindPDU)

	c.bound = false
	close(c.done)

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsBound returns whether the client is currently bound to the SMSC.
func (c *Client) IsBound() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bound
}

// writePDU encodes and writes a PDU to the connection. Caller must handle locking
// if exclusive access is needed; this method is safe for concurrent use because
// net.Conn.Write is thread-safe per the Go docs.
func (c *Client) writePDU(pdu *PDU) error {
	data := EncodePDU(pdu)
	_, err := c.conn.Write(data)
	if err != nil {
		c.logger.Error("failed to write PDU",
			zap.Uint32("command_id", pdu.CommandID),
			zap.Error(err),
		)
	}
	return err
}

// registerPending creates a channel for receiving the response to a request
// with the given sequence number.
func (c *Client) registerPending(seq uint32) chan *PDU {
	ch := make(chan *PDU, 1)
	c.pendingMu.Lock()
	c.pending[seq] = ch
	c.pendingMu.Unlock()
	return ch
}

// unregisterPending removes and returns the pending response channel for a
// given sequence number.
func (c *Client) unregisterPending(seq uint32) chan *PDU {
	c.pendingMu.Lock()
	ch := c.pending[seq]
	delete(c.pending, seq)
	c.pendingMu.Unlock()
	return ch
}

// ReadLoop continuously reads PDUs from the connection and dispatches them.
func (c *Client) ReadLoop() {
	headerBuf := make([]byte, pduHeaderLen)

	for {
		select {
		case <-c.done:
			return
		default:
		}

		// Read PDU header (16 bytes).
		_, err := io.ReadFull(c.conn, headerBuf)
		if err != nil {
			select {
			case <-c.done:
				return // connection closed intentionally
			default:
			}
			c.logger.Error("failed to read PDU header", zap.Error(err))
			c.handleDisconnect()
			return
		}

		cmdLen := binary.BigEndian.Uint32(headerBuf[0:4])
		if cmdLen < pduHeaderLen {
			c.logger.Error("invalid PDU command_length", zap.Uint32("length", cmdLen))
			c.handleDisconnect()
			return
		}

		// Read the rest of the PDU body.
		fullPDU := make([]byte, cmdLen)
		copy(fullPDU, headerBuf)
		if cmdLen > pduHeaderLen {
			_, err := io.ReadFull(c.conn, fullPDU[pduHeaderLen:])
			if err != nil {
				c.logger.Error("failed to read PDU body", zap.Error(err))
				c.handleDisconnect()
				return
			}
		}

		pdu, err := DecodePDU(fullPDU)
		if err != nil {
			c.logger.Error("failed to decode PDU", zap.Error(err))
			continue
		}

		c.dispatchPDU(pdu)
	}
}

// dispatchPDU routes an incoming PDU to the correct handler.
func (c *Client) dispatchPDU(pdu *PDU) {
	switch pdu.CommandID {
	case CmdBindTransceiverResp, CmdBindTransmitterResp, CmdBindReceiverResp,
		CmdSubmitSMResp, CmdUnbindResp,
		CmdQuerySMResp, CmdCancelSMResp, CmdReplaceSMResp,
		CmdDataSMResp, CmdSubmitMultiResp:
		// Response to a request we sent -- deliver to the pending channel.
		ch := c.unregisterPending(pdu.SequenceNumber)
		if ch != nil {
			ch <- pdu
		} else {
			c.logger.Warn("received response for unknown sequence",
				zap.Uint32("command_id", pdu.CommandID),
				zap.Uint32("sequence", pdu.SequenceNumber),
			)
		}

	case CmdDeliverSM:
		// Incoming deliver_sm: queue for handler. The deliver_sm_resp is sent
		// AFTER the handler completes, so data is durable before we ACK the SMSC.
		sourceAddr, destAddr, esmClass, shortMessage := ParseDeliverSM(pdu.Body)

		c.enqueueDeliver(deliverMessage{
			sourceAddr: sourceAddr,
			destAddr:   destAddr,
			esmClass:   esmClass,
			payload:    append([]byte(nil), shortMessage...),
			seqNum:     pdu.SequenceNumber,
		})

	case CmdEnquireLink:
		// Respond to enquire_link from SMSC.
		resp := &PDU{
			CommandID:      CmdEnquireLinkResp,
			CommandStatus:  StatusOK,
			SequenceNumber: pdu.SequenceNumber,
		}
		if err := c.writePDU(resp); err != nil {
			c.logger.Error("failed to send enquire_link_resp", zap.Error(err))
		}

	case CmdEnquireLinkResp:
		// Response to our enquire_link; nothing to do, connection is alive.
		c.logger.Debug("enquire_link_resp received")

	case CmdUnbind:
		// SMSC is unbinding us.
		resp := &PDU{
			CommandID:      CmdUnbindResp,
			CommandStatus:  StatusOK,
			SequenceNumber: pdu.SequenceNumber,
		}
		_ = c.writePDU(resp)
		c.handleDisconnect()

	default:
		c.logger.Warn("unhandled PDU command",
			zap.Uint32("command_id", pdu.CommandID),
			zap.Uint32("sequence", pdu.SequenceNumber),
		)
	}
}

// enquireLinkLoop sends periodic enquire_link PDUs to keep the connection alive.
func (c *Client) enquireLinkLoop() {
	ticker := time.NewTicker(time.Duration(c.config.EnquireLinkSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			seq := c.nextSeq()
			pdu := &PDU{
				CommandID:      CmdEnquireLink,
				CommandStatus:  StatusOK,
				SequenceNumber: seq,
			}
			if err := c.writePDU(pdu); err != nil {
				c.logger.Error("failed to send enquire_link", zap.Error(err))
				return
			}
			c.logger.Debug("enquire_link sent", zap.Uint32("sequence", seq))
		}
	}
}

func (c *Client) startDeliverLoop() {
	for i := 0; i < c.deliverWorkers; i++ {
		go func(done <-chan struct{}, q <-chan deliverMessage) {
			for {
				select {
				case <-done:
					return
				case msg := <-q:
					var handlerErr error
					if c.handler != nil {
						handlerErr = c.handler(msg.sourceAddr, msg.destAddr, msg.esmClass, msg.payload)
					}

					// Send deliver_sm_resp AFTER handler completes.
					status := StatusOK
					if handlerErr != nil {
						status = StatusSysErr
						c.logger.Warn("deliver handler failed, NACKing SMSC",
							zap.Uint32("seq", msg.seqNum),
							zap.Error(handlerErr),
						)
					}
					resp := EncodeDeliverSMRespWithStatus(msg.seqNum, status)
					if err := c.writePDU(resp); err != nil {
						c.logger.Error("failed to send deliver_sm_resp", zap.Error(err))
					}
				}
			}
		}(c.done, c.deliverQ)
	}
}

func (c *Client) enqueueDeliver(msg deliverMessage) {
	select {
	case <-c.done:
		return
	case c.deliverQ <- msg:
		return
	default:
	}

	// Queue is full — block until space is available. This applies natural
	// TCP backpressure to the SMSC: the read loop stalls, the TCP window
	// fills, and the SMSC throttles delivery. This is the correct behavior
	// for a lossless return path — DLR/MO messages must never be dropped.
	if c.deliverBackpressure != nil {
		c.deliverBackpressure.Add(context.Background(), 1)
	}
	c.logger.Warn("deliver queue full, blocking until space available",
		zap.Int("queue_len", len(c.deliverQ)),
		zap.Int("queue_cap", cap(c.deliverQ)),
	)

	select {
	case <-c.done:
		return
	case c.deliverQ <- msg:
		return
	}
}

func (c *Client) handleDisconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.bound {
		return
	}

	c.logger.Warn("SMPP connection lost")
	c.bound = false

	// Signal all goroutines to stop.
	select {
	case <-c.done:
		// Already closed.
	default:
		close(c.done)
	}

	// Drain all pending requests with an error.
	c.pendingMu.Lock()
	for seq, ch := range c.pending {
		ch <- &PDU{
			CommandID:     CmdGenericNack,
			CommandStatus: StatusSysErr,
		}
		delete(c.pending, seq)
	}
	c.pendingMu.Unlock()

	if c.conn != nil {
		_ = c.conn.Close()
	}
}
