package gateway

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/idnteq/go-smsc/smpp"
)

// Connection represents a single northbound SMPP session from an OTA Engine.
type Connection struct {
	ID        string
	SystemID  string
	BindMode  smpp.BindMode // bind mode negotiated during bind
	conn      net.Conn
	writeMu   sync.Mutex
	bound     bool
	seqNum    atomic.Uint32
	createdAt time.Time
	logger    *zap.Logger

	// pendingDelivers tracks outstanding deliver_sm sent to this engine,
	// keyed by sequence number. The channel is signalled when deliver_sm_resp
	// is received.
	pendingDelivers sync.Map // uint32 → chan uint32 (status code)

	// Activity tracking for stale connection detection.
	lastActivity atomic.Int64 // Unix nanoseconds of last PDU received

	// Per-connection throughput tracking.
	submitCount atomic.Uint64 // total submits from this connection
	tpsWindow   atomic.Int64  // start of current 1-second window (Unix seconds)
	tpsCount    atomic.Uint64 // submits in current window

	// In-flight tracking for graceful shutdown.
	inFlight atomic.Int32
}

// SubmitHandler is called when the engine sends a submit_sm.
type SubmitHandler func(connID string, seqNum uint32, body []byte)

// DeliverRespHandler is called when the engine sends a deliver_sm_resp.
type DeliverRespHandler func(connID string, seqNum uint32, status uint32)

// BindHandler is called when an engine completes bind_transceiver.
// The server uses this to assign the stable connection ID (system_id).
type BindHandler func(c *Connection, systemID string)

// NewConnection creates a connection wrapper.
func NewConnection(id string, conn net.Conn, logger *zap.Logger) *Connection {
	c := &Connection{
		ID:        id,
		conn:      conn,
		createdAt: time.Now(),
		logger:    logger.With(zap.String("conn_id", id)),
	}
	c.lastActivity.Store(time.Now().UnixNano())
	return c
}

// BindConfig holds bind-time validation parameters passed from the server.
type BindConfig struct {
	Password       string   // required password (empty = accept any)
	AllowedEngines []string // system_id whitelist (empty = allow all)
	ServerSystemID string   // system_id to present in bind_resp
	SMPPVersion    string   // "3.4" or "5.0" — when "5.0", include sc_interface_version TLV in bind_resp
}

// ReadLoop reads PDUs from the engine and dispatches them. It blocks until
// the connection is closed or an error occurs.
func (c *Connection) ReadLoop(
	done <-chan struct{},
	bindCfg BindConfig,
	onSubmit SubmitHandler,
	onDeliverResp DeliverRespHandler,
	onBind BindHandler,
) {
	headerBuf := make([]byte, 16)
	for {
		select {
		case <-done:
			return
		default:
		}

		_, err := io.ReadFull(c.conn, headerBuf)
		if err != nil {
			if err != io.EOF {
				select {
				case <-done:
					return
				default:
				}
				c.logger.Debug("read error", zap.Error(err))
			}
			return
		}

		cmdLen := binary.BigEndian.Uint32(headerBuf[0:4])
		if cmdLen < 16 {
			c.logger.Error("invalid PDU command_length", zap.Uint32("length", cmdLen))
			return
		}

		fullPDU := make([]byte, cmdLen)
		copy(fullPDU, headerBuf)
		if cmdLen > 16 {
			if _, err := io.ReadFull(c.conn, fullPDU[16:]); err != nil {
				c.logger.Error("failed to read PDU body", zap.Error(err))
				return
			}
		}

		pdu, err := smpp.DecodePDU(fullPDU)
		if err != nil {
			c.logger.Error("failed to decode PDU", zap.Error(err))
			return
		}

		// Track activity for stale detection and keepalive.
		c.lastActivity.Store(time.Now().UnixNano())

		switch pdu.CommandID {
		case smpp.CmdBindTransceiver, smpp.CmdBindTransmitter, smpp.CmdBindReceiver:
			if c.handleBind(pdu, bindCfg) && onBind != nil {
				onBind(c, c.SystemID)
			}

		case smpp.CmdSubmitSM:
			if !c.bound {
				_ = c.sendResponse(smpp.CmdSubmitSMResp, smpp.StatusInvBnd, pdu.SequenceNumber, []byte{0x00})
				continue
			}
			onSubmit(c.ID, pdu.SequenceNumber, pdu.Body)

		case smpp.CmdEnquireLink:
			_ = c.sendResponse(smpp.CmdEnquireLinkResp, smpp.StatusOK, pdu.SequenceNumber, nil)

		case smpp.CmdDeliverSMResp:
			onDeliverResp(c.ID, pdu.SequenceNumber, pdu.CommandStatus)

		case smpp.CmdUnbind:
			_ = c.sendResponse(smpp.CmdUnbindResp, smpp.StatusOK, pdu.SequenceNumber, nil)
			c.logger.Info("engine unbound")
			return

		default:
			c.logger.Warn("unhandled PDU", zap.Uint32("command_id", pdu.CommandID))
			_ = c.sendResponse(smpp.CmdGenericNack, smpp.StatusInvCmdID, pdu.SequenceNumber, nil)
		}
	}
}

// bindRespCommandID returns the appropriate bind response command ID for
// the given bind request command ID.
func bindRespCommandID(cmdID uint32) uint32 {
	switch cmdID {
	case smpp.CmdBindTransmitter:
		return smpp.CmdBindTransmitterResp
	case smpp.CmdBindReceiver:
		return smpp.CmdBindReceiverResp
	default:
		return smpp.CmdBindTransceiverResp
	}
}

// bindModeFromCommandID maps a bind request command ID to an smpp.BindMode.
func bindModeFromCommandID(cmdID uint32) smpp.BindMode {
	switch cmdID {
	case smpp.CmdBindTransmitter:
		return smpp.BindTransmitter
	case smpp.CmdBindReceiver:
		return smpp.BindReceiver
	default:
		return smpp.BindTransceiver
	}
}

// handleBind processes a bind request (transceiver, transmitter, or receiver) from the engine.
// Returns true if the bind succeeded.
func (c *Connection) handleBind(pdu *smpp.PDU, cfg BindConfig) bool {
	systemID, offset := readCString(pdu.Body, 0)
	password, _ := readCString(pdu.Body, offset)

	respCmdID := bindRespCommandID(pdu.CommandID)

	c.SystemID = systemID
	c.logger = c.logger.With(zap.String("system_id", systemID))

	if cfg.Password != "" && password != cfg.Password {
		c.logger.Warn("bind rejected: invalid password")
		_ = c.sendResponse(respCmdID, smpp.StatusInvBnd, pdu.SequenceNumber, []byte{0x00})
		return false
	}

	// Check system_id whitelist if configured.
	if len(cfg.AllowedEngines) > 0 {
		allowed := false
		for _, a := range cfg.AllowedEngines {
			if a == systemID {
				allowed = true
				break
			}
		}
		if !allowed {
			c.logger.Warn("bind rejected: system_id not in allowed list")
			_ = c.sendResponse(respCmdID, smpp.StatusInvBnd, pdu.SequenceNumber, []byte{0x00})
			return false
		}
	}

	// Build bind response body: system_id C-string + optional TLVs.
	serverID := cfg.ServerSystemID
	if serverID == "" {
		serverID = "SMSCGW"
	}
	respBody := writeCStringBytes(serverID)

	// When configured for SMPP 5.0, include sc_interface_version TLV (0x0210)
	// in the bind response to advertise 5.0 support.
	if cfg.SMPPVersion == "5.0" {
		tlvs := make(smpp.TLVSet)
		tlvs.SetUint8(smpp.TagSCInterfaceVersion, 0x50)
		respBody = append(respBody, tlvs.Encode()...)
	}

	_ = c.sendResponse(respCmdID, smpp.StatusOK, pdu.SequenceNumber, respBody)
	c.BindMode = bindModeFromCommandID(pdu.CommandID)
	c.bound = true
	return true
}

// SendSubmitSMResp sends a submit_sm_resp back to the engine.
func (c *Connection) SendSubmitSMResp(seqNum uint32, status uint32, messageID string) error {
	body := writeCStringBytes(messageID)
	return c.sendResponse(smpp.CmdSubmitSMResp, status, seqNum, body)
}

// SendDeliverSM sends a deliver_sm to the engine and returns the sequence
// number used. The caller can wait for the ACK via WaitDeliverResp.
func (c *Connection) SendDeliverSM(body []byte) (uint32, error) {
	seq := c.seqNum.Add(1)
	pdu := &smpp.PDU{
		CommandID:      smpp.CmdDeliverSM,
		CommandStatus:  smpp.StatusOK,
		SequenceNumber: seq,
		Body:           body,
	}
	if err := c.WritePDU(pdu); err != nil {
		return 0, err
	}
	return seq, nil
}

// RegisterDeliverWait creates a channel that will receive the deliver_sm_resp
// status for the given sequence number.
func (c *Connection) RegisterDeliverWait(seqNum uint32) chan uint32 {
	ch := make(chan uint32, 1)
	c.pendingDelivers.Store(seqNum, ch)
	return ch
}

// ResolveDeliverResp signals a pending deliver_sm_resp wait.
func (c *Connection) ResolveDeliverResp(seqNum uint32, status uint32) {
	if v, ok := c.pendingDelivers.LoadAndDelete(seqNum); ok {
		ch := v.(chan uint32)
		ch <- status
	}
}

// WritePDU encodes and writes a PDU to the engine, serialized via mutex.
func (c *Connection) WritePDU(pdu *smpp.PDU) error {
	data := smpp.EncodePDU(pdu)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.conn.Write(data)
	return err
}

// Close closes the underlying TCP connection.
func (c *Connection) Close() error {
	return c.conn.Close()
}

// IsBound returns whether this connection has completed bind_transceiver.
func (c *Connection) IsBound() bool {
	return c.bound
}

// LastActivity returns when the last PDU was received on this connection.
func (c *Connection) LastActivity() time.Time {
	return time.Unix(0, c.lastActivity.Load())
}

// RecordSubmit increments the per-connection submit counter and TPS tracker.
func (c *Connection) RecordSubmit() {
	c.submitCount.Add(1)

	now := time.Now().Unix()
	window := c.tpsWindow.Load()
	if now != window {
		// New second window — reset counter.
		c.tpsWindow.Store(now)
		c.tpsCount.Store(1)
	} else {
		c.tpsCount.Add(1)
	}
}

// CurrentTPS returns the number of submits in the current 1-second window.
func (c *Connection) CurrentTPS() uint64 {
	now := time.Now().Unix()
	if c.tpsWindow.Load() == now {
		return c.tpsCount.Load()
	}
	return 0
}

// TotalSubmits returns the total number of submits from this connection.
func (c *Connection) TotalSubmits() uint64 {
	return c.submitCount.Load()
}

// InFlightAdd adjusts the in-flight counter. Call with +1 before processing,
// -1 after completing a submit forward.
func (c *Connection) InFlightAdd(delta int32) {
	c.inFlight.Add(delta)
}

// InFlightCount returns the number of in-flight operations.
func (c *Connection) InFlightCount() int32 {
	return c.inFlight.Load()
}

func (c *Connection) sendResponse(cmdID, status, seqNum uint32, body []byte) error {
	pdu := &smpp.PDU{
		CommandID:      cmdID,
		CommandStatus:  status,
		SequenceNumber: seqNum,
		Body:           body,
	}
	return c.WritePDU(pdu)
}

// readCString reads a null-terminated string from data starting at offset.
// Delegates to the exported smpp.ReadCString.
func readCString(data []byte, offset int) (string, int) {
	return smpp.ReadCString(data, offset)
}

// writeCStringBytes creates a byte slice containing a null-terminated string.
// Delegates to the exported smpp.WriteCStringBytes.
func writeCStringBytes(s string) []byte {
	return smpp.WriteCStringBytes(s)
}

// ParseSubmitSMAddresses extracts source and destination addresses from a
// submit_sm body. This is the minimal parsing needed for routing; the full
// body is forwarded unmodified to the downstream SMSC.
func ParseSubmitSMAddresses(body []byte) (sourceAddr, destAddr string) {
	if len(body) == 0 {
		return "", ""
	}
	offset := 0
	// service_type (C-string)
	_, offset = readCString(body, offset)
	// source_addr_ton
	if offset >= len(body) {
		return
	}
	offset++
	// source_addr_npi
	if offset >= len(body) {
		return
	}
	offset++
	// source_addr
	sourceAddr, offset = readCString(body, offset)
	// dest_addr_ton
	if offset >= len(body) {
		return
	}
	offset++
	// dest_addr_npi
	if offset >= len(body) {
		return
	}
	offset++
	// destination_addr
	destAddr, _ = readCString(body, offset)
	return sourceAddr, destAddr
}

// BuildDeliverSMBody constructs a deliver_sm body for forwarding.
// TODO: refactor to use smpp.TLVSet for the message_payload TLV instead of raw byte
// manipulation. This would require changing the return type or approach since
// TLVs should ideally be set on the PDU's TLVs field rather than embedded in Body.
func BuildDeliverSMBody(sourceAddr, destAddr string, esmClass byte, shortMessage []byte) []byte {
	// Estimate buffer size: addresses + fixed fields + message
	size := len(sourceAddr) + len(destAddr) + 20 + len(shortMessage)
	buf := make([]byte, 0, size)

	buf = appendCString(buf, "")          // service_type
	buf = append(buf, 0x00, 0x00)         // source_addr_ton, source_addr_npi
	buf = appendCString(buf, sourceAddr)  // source_addr
	buf = append(buf, 0x01, 0x01)         // dest_addr_ton (international), dest_addr_npi (ISDN)
	buf = appendCString(buf, destAddr)    // destination_addr
	buf = append(buf, esmClass)           // esm_class
	buf = append(buf, 0x00)               // protocol_id
	buf = append(buf, 0x00)               // priority_flag
	buf = appendCString(buf, "")          // schedule_delivery_time
	buf = appendCString(buf, "")          // validity_period
	buf = append(buf, 0x00)               // registered_delivery
	buf = append(buf, 0x00)               // replace_if_present_flag
	buf = append(buf, 0x00)               // data_coding
	buf = append(buf, 0x00)               // sm_default_msg_id

	if len(shortMessage) > 254 {
		buf = append(buf, 0x00) // sm_length = 0
		// message_payload TLV (tag=0x0424)
		buf = append(buf, 0x04, 0x24)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(shortMessage)))
		buf = append(buf, lenBuf...)
		buf = append(buf, shortMessage...)
	} else {
		buf = append(buf, byte(len(shortMessage)))
		buf = append(buf, shortMessage...)
	}

	return buf
}

// BuildSubmitSMBody constructs a minimal submit_sm body from source/dest
// addresses and message text. Used by the REST API to inject messages into
// the forwarding pipeline.
// TODO: refactor to use smpp.TLVSet for the message_payload TLV instead of raw byte
// manipulation. This would require changing the return type or approach since
// TLVs should ideally be set on the PDU's TLVs field rather than embedded in Body.
func BuildSubmitSMBody(sourceAddr, destAddr string, message []byte) []byte {
	size := len(sourceAddr) + len(destAddr) + 20 + len(message)
	buf := make([]byte, 0, size)

	buf = appendCString(buf, "")          // service_type
	buf = append(buf, 0x00, 0x00)         // source_addr_ton, source_addr_npi
	buf = appendCString(buf, sourceAddr)  // source_addr
	buf = append(buf, 0x01, 0x01)         // dest_addr_ton (international), dest_addr_npi (ISDN)
	buf = appendCString(buf, destAddr)    // destination_addr
	buf = append(buf, 0x00)               // esm_class
	buf = append(buf, 0x00)               // protocol_id
	buf = append(buf, 0x00)               // priority_flag
	buf = appendCString(buf, "")          // schedule_delivery_time
	buf = appendCString(buf, "")          // validity_period
	buf = append(buf, 0x01)               // registered_delivery (request DLR)
	buf = append(buf, 0x00)               // replace_if_present_flag
	buf = append(buf, 0x00)               // data_coding
	buf = append(buf, 0x00)               // sm_default_msg_id

	if len(message) > 254 {
		buf = append(buf, 0x00) // sm_length = 0
		// message_payload TLV (tag=0x0424)
		buf = append(buf, 0x04, 0x24)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(message)))
		buf = append(buf, lenBuf...)
		buf = append(buf, message...)
	} else {
		buf = append(buf, byte(len(message)))
		buf = append(buf, message...)
	}

	return buf
}

func appendCString(buf []byte, s string) []byte {
	buf = append(buf, []byte(s)...)
	buf = append(buf, 0x00)
	return buf
}

// ParseBindTransceiver extracts system_id and password from bind body.
func ParseBindTransceiver(body []byte) (systemID, password string) {
	systemID, offset := readCString(body, 0)
	password, _ = readCString(body, offset)
	return
}

// EncodeBindTransceiverResp builds a bind_transceiver_resp body.
func EncodeBindTransceiverResp(systemID string) []byte {
	return writeCStringBytes(systemID)
}

// EncodeSubmitSMResp builds a submit_sm_resp body with message_id.
func EncodeSubmitSMResp(messageID string) []byte {
	return writeCStringBytes(messageID)
}

// ParseSubmitSMRespBody extracts message_id from submit_sm_resp body.
func ParseSubmitSMRespBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	msgID, _ := readCString(body, 0)
	return msgID
}

// FormatError returns an SMPP error description for common status codes.
func FormatError(status uint32) string {
	switch status {
	case smpp.StatusOK:
		return "OK"
	case smpp.StatusInvBnd:
		return "invalid bind"
	case smpp.StatusSysErr:
		return "system error"
	case smpp.StatusThrottled:
		return "throttled"
	case smpp.StatusMsgQFull:
		return "message queue full"
	case smpp.StatusSubmitFail:
		return "submit failed"
	default:
		return fmt.Sprintf("status 0x%08X", status)
	}
}
