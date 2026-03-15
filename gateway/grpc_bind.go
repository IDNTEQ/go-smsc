package gateway

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/idnteq/go-smsc/smpp"

	smscv1 "github.com/idnteq/go-smsc/proto/smscv1"
)

// GRPCBind connects to a gRPC SMS adapter and implements the Submitter interface.
type GRPCBind struct {
	name    string
	addr    string
	conn    *grpc.ClientConn
	client  smscv1.SMSAdapterServiceClient
	handler func(sourceAddr, destAddr string, esmClass byte, payload []byte) error
	logger  *zap.Logger
	cancel  context.CancelFunc

	healthy     atomic.Bool
	submitCount atomic.Int64
}

// NewGRPCBind creates a new gRPC bind client. The handler receives MO/DLR
// deliveries from the adapter (same signature as smpp.DeliverHandler).
func NewGRPCBind(name, addr string, handler func(string, string, byte, []byte) error, logger *zap.Logger) *GRPCBind {
	return &GRPCBind{
		name:    name,
		addr:    addr,
		handler: handler,
		logger:  logger.Named("grpc-bind").With(zap.String("name", name)),
	}
}

// Connect dials the gRPC server and starts the StreamDeliveries goroutine.
func (b *GRPCBind) Connect(ctx context.Context) error {
	conn, err := grpc.NewClient(b.addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("grpc dial %s: %w", b.addr, err)
	}
	b.conn = conn
	b.client = smscv1.NewSMSAdapterServiceClient(conn)

	// Verify connectivity with an initial GetStatus call.
	statusCtx, statusCancel := context.WithTimeout(ctx, 10*time.Second)
	defer statusCancel()

	status, err := b.client.GetStatus(statusCtx, &smscv1.GetStatusRequest{})
	if err != nil {
		_ = conn.Close()
		b.conn = nil
		b.client = nil
		return fmt.Errorf("grpc GetStatus %s: %w", b.addr, err)
	}
	base := status.GetBase()
	if base != nil {
		b.healthy.Store(base.GetHealthy())
	}

	b.logger.Info("gRPC adapter connected",
		zap.String("addr", b.addr),
		zap.String("adapter_id", base.GetAdapterId()),
		zap.Bool("healthy", base.GetHealthy()),
	)

	// Start stream deliveries in the background.
	streamCtx, streamCancel := context.WithCancel(context.Background())
	b.cancel = streamCancel
	go b.streamDeliveries(streamCtx)
	go b.healthCheckLoop(streamCtx)

	return nil
}

// SubmitRaw parses the raw submit_sm body into structured fields and calls
// the gRPC SubmitMT RPC.
func (b *GRPCBind) SubmitRaw(body []byte) (*smpp.SubmitResponse, error) {
	if b.client == nil {
		return nil, fmt.Errorf("grpc bind %q not connected", b.name)
	}

	parsed := parseSubmitSMBody(body)

	req := &smscv1.SubmitMTRequest{
		SourceAddr:    parsed.sourceAddr,
		SourceAddrTon: uint32(parsed.sourceTON),
		SourceAddrNpi: uint32(parsed.sourceNPI),
		DestAddr:      parsed.destAddr,
		DestAddrTon:   uint32(parsed.destTON),
		DestAddrNpi:   uint32(parsed.destNPI),
		EsmClass:      uint32(parsed.esmClass),
		ProtocolId:    uint32(parsed.protocolID),
		DataCoding:    uint32(parsed.dataCoding),
		RegisterDlr:   parsed.registeredDelivery != 0,
		Payload:       parsed.shortMessage,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := b.client.SubmitMT(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("grpc SubmitMT: %w", err)
	}

	b.submitCount.Add(1)

	if resp.GetStatus() != 0 {
		return &smpp.SubmitResponse{
			MessageID: resp.GetMessageId(),
			Error:     fmt.Errorf("adapter rejected: status=%d msg=%s", resp.GetStatus(), resp.GetErrorMessage()),
		}, nil
	}

	return &smpp.SubmitResponse{
		MessageID: resp.GetMessageId(),
	}, nil
}

// ActiveConnections returns 1 if connected, 0 if not.
func (b *GRPCBind) ActiveConnections() int {
	if b.conn != nil && b.healthy.Load() {
		return 1
	}
	return 0
}

// IsHealthy returns the healthy flag (set by periodic GetStatus calls).
func (b *GRPCBind) IsHealthy() bool {
	return b.healthy.Load()
}

// Close cancels background goroutines and closes the gRPC connection.
func (b *GRPCBind) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	b.healthy.Store(false)
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

// BindType returns "grpc".
func (b *GRPCBind) BindType() string {
	return "grpc"
}

// streamDeliveries opens a server-streaming RPC and dispatches incoming
// MO/DLR/alert messages via the handler callback.
func (b *GRPCBind) streamDeliveries(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stream, err := b.client.StreamDeliveries(ctx, &smscv1.StreamDeliveriesRequest{
			ClientId:    b.name,
			MaxInflight: 100,
		})
		if err != nil {
			b.logger.Warn("StreamDeliveries failed, retrying in 5s", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		b.logger.Info("StreamDeliveries connected")

		for {
			msg, err := stream.Recv()
			if err != nil {
				if err == io.EOF || ctx.Err() != nil {
					return
				}
				b.logger.Warn("StreamDeliveries recv error, reconnecting", zap.Error(err))
				break
			}

			b.dispatchDelivery(msg)
		}
	}
}

// dispatchDelivery routes a DeliveryMessage to the handler callback.
func (b *GRPCBind) dispatchDelivery(msg *smscv1.DeliveryMessage) {
	if b.handler == nil {
		return
	}

	switch msg.GetType() {
	case smscv1.DeliveryType_MO_SMS:
		if err := b.handler(msg.GetSourceAddr(), msg.GetDestAddr(), byte(msg.GetEsmClass()), msg.GetPayload()); err != nil {
			b.logger.Warn("MO handler error", zap.Error(err))
		}
	case smscv1.DeliveryType_DLR:
		// DLR: set esm_class bit 0x04 for DLR indication.
		esmClass := byte(msg.GetEsmClass())
		if esmClass&0x04 == 0 {
			esmClass |= 0x04
		}
		// Build DLR receipt text from structured fields.
		payload := msg.GetPayload()
		if len(payload) == 0 && msg.GetOriginalMessageId() != "" {
			receipt := fmt.Sprintf("id:%s sub:001 dlvrd:001 submit date:%s done date:%s stat:%s err:%s text:",
				msg.GetOriginalMessageId(),
				msg.GetDlrDoneDate(),
				msg.GetDlrDoneDate(),
				msg.GetDlrStatus(),
				msg.GetDlrErrorCode(),
			)
			payload = []byte(receipt)
		}
		if err := b.handler(msg.GetSourceAddr(), msg.GetDestAddr(), esmClass, payload); err != nil {
			b.logger.Warn("DLR handler error", zap.Error(err))
		}
	case smscv1.DeliveryType_ALERT:
		b.logger.Info("adapter alert received",
			zap.String("msisdn", msg.GetAlertMsisdn()),
			zap.String("delivery_id", msg.GetDeliveryId()),
		)
	default:
		b.logger.Warn("unknown delivery type", zap.Int32("type", int32(msg.GetType())))
	}
}

// healthCheckLoop periodically polls GetStatus to update the healthy flag.
func (b *GRPCBind) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			status, err := b.client.GetStatus(checkCtx, &smscv1.GetStatusRequest{})
			cancel()
			if err != nil {
				b.healthy.Store(false)
				b.logger.Warn("health check failed", zap.Error(err))
				continue
			}
			if status.GetBase() != nil {
				b.healthy.Store(status.GetBase().GetHealthy())
			}
		}
	}
}

// parsedSubmitSM holds the parsed fields from a raw submit_sm body.
type parsedSubmitSM struct {
	sourceAddr          string
	sourceTON           byte
	sourceNPI           byte
	destAddr            string
	destTON             byte
	destNPI             byte
	esmClass            byte
	protocolID          byte
	dataCoding          byte
	registeredDelivery  byte
	shortMessage        []byte
}

// parseSubmitSMBody parses a raw submit_sm PDU body into structured fields.
// Field layout (SMPP 3.4 spec):
//
//	service_type      C-String
//	source_addr_ton   1 byte
//	source_addr_npi   1 byte
//	source_addr       C-String
//	dest_addr_ton     1 byte
//	dest_addr_npi     1 byte
//	destination_addr  C-String
//	esm_class         1 byte
//	protocol_id       1 byte
//	priority_flag     1 byte
//	schedule_delivery_time C-String
//	validity_period    C-String
//	registered_delivery 1 byte
//	replace_if_present  1 byte
//	data_coding         1 byte
//	sm_default_msg_id   1 byte
//	sm_length           1 byte
//	short_message       sm_length bytes
func parseSubmitSMBody(body []byte) parsedSubmitSM {
	var p parsedSubmitSM
	if len(body) == 0 {
		return p
	}

	offset := 0

	// service_type (C-string)
	_, offset = smpp.ReadCString(body, offset)
	if offset >= len(body) {
		return p
	}

	// source_addr_ton
	p.sourceTON = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// source_addr_npi
	p.sourceNPI = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// source_addr
	p.sourceAddr, offset = smpp.ReadCString(body, offset)
	if offset >= len(body) {
		return p
	}

	// dest_addr_ton
	p.destTON = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// dest_addr_npi
	p.destNPI = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// destination_addr
	p.destAddr, offset = smpp.ReadCString(body, offset)
	if offset >= len(body) {
		return p
	}

	// esm_class
	p.esmClass = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// protocol_id
	p.protocolID = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// priority_flag (skip)
	offset++
	if offset >= len(body) {
		return p
	}

	// schedule_delivery_time (C-string, skip)
	_, offset = smpp.ReadCString(body, offset)
	if offset >= len(body) {
		return p
	}

	// validity_period (C-string, skip)
	_, offset = smpp.ReadCString(body, offset)
	if offset >= len(body) {
		return p
	}

	// registered_delivery
	p.registeredDelivery = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// replace_if_present_flag (skip)
	offset++
	if offset >= len(body) {
		return p
	}

	// data_coding
	p.dataCoding = body[offset]
	offset++
	if offset >= len(body) {
		return p
	}

	// sm_default_msg_id (skip)
	offset++
	if offset >= len(body) {
		return p
	}

	// sm_length
	smLength := int(body[offset])
	offset++

	// short_message
	if smLength > 0 && offset+smLength <= len(body) {
		p.shortMessage = body[offset : offset+smLength]
	} else if smLength == 0 && offset < len(body) {
		// sm_length == 0 may indicate message_payload TLV follows.
		// Try to extract message_payload TLV (tag 0x0424).
		p.shortMessage = extractMessagePayloadTLV(body[offset:])
	}

	return p
}

// extractMessagePayloadTLV scans for the message_payload TLV (tag 0x0424)
// in a raw TLV region and returns the value, or nil if not found.
func extractMessagePayloadTLV(data []byte) []byte {
	offset := 0
	for offset+4 <= len(data) {
		tag := uint16(data[offset])<<8 | uint16(data[offset+1])
		tlvLen := int(uint16(data[offset+2])<<8 | uint16(data[offset+3]))
		offset += 4
		if offset+tlvLen > len(data) {
			return nil
		}
		if tag == 0x0424 { // message_payload
			return data[offset : offset+tlvLen]
		}
		offset += tlvLen
	}
	return nil
}
