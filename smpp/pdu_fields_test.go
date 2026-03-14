package smpp

import (
	"encoding/binary"
	"testing"
)

// =============================================================================
// MandatoryBodyLen tests
// =============================================================================

func TestMandatoryBodyLen_SubmitSM(t *testing.T) {
	body := buildDeliverSMBody("+27830001234", "GATEWAY", 0x00, []byte("Hello world"))
	got := MandatoryBodyLen(CmdSubmitSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(submit_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_DeliverSM(t *testing.T) {
	body := buildDeliverSMBody("+27830001234", "GATEWAY", 0x04, []byte("id:GW-1 stat:DELIVRD"))
	got := MandatoryBodyLen(CmdDeliverSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(deliver_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_SubmitSMResp(t *testing.T) {
	body := []byte("MSG-001\x00")
	got := MandatoryBodyLen(CmdSubmitSMResp, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(submit_sm_resp) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_DeliverSMResp(t *testing.T) {
	body := []byte{0x00} // empty message_id
	got := MandatoryBodyLen(CmdDeliverSMResp, body)
	if got != 1 {
		t.Errorf("MandatoryBodyLen(deliver_sm_resp) = %d, want 1", got)
	}
}

func TestMandatoryBodyLen_BindTransceiver(t *testing.T) {
	body := EncodeBindTransceiver("test_user", "secret", "")
	got := MandatoryBodyLen(CmdBindTransceiver, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(bind_transceiver) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_BindTransmitter(t *testing.T) {
	body := EncodeBindTransceiver("tx_user", "pass", "type1")
	got := MandatoryBodyLen(CmdBindTransmitter, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(bind_transmitter) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_BindReceiver(t *testing.T) {
	body := EncodeBindTransceiver("rx_user", "pass", "")
	got := MandatoryBodyLen(CmdBindReceiver, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(bind_receiver) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_BindTransceiverResp(t *testing.T) {
	body := []byte("SMSCGW\x00")
	got := MandatoryBodyLen(CmdBindTransceiverResp, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(bind_transceiver_resp) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_EnquireLink(t *testing.T) {
	got := MandatoryBodyLen(CmdEnquireLink, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(enquire_link) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_EnquireLinkResp(t *testing.T) {
	got := MandatoryBodyLen(CmdEnquireLinkResp, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(enquire_link_resp) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_Unbind(t *testing.T) {
	got := MandatoryBodyLen(CmdUnbind, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(unbind) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_UnbindResp(t *testing.T) {
	got := MandatoryBodyLen(CmdUnbindResp, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(unbind_resp) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_GenericNack(t *testing.T) {
	got := MandatoryBodyLen(CmdGenericNack, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(generic_nack) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_UnknownCommand(t *testing.T) {
	got := MandatoryBodyLen(0xDEADBEEF, []byte{0x01, 0x02})
	if got != -1 {
		t.Errorf("MandatoryBodyLen(unknown) = %d, want -1", got)
	}
}

func TestMandatoryBodyLen_TruncatedSubmitSM(t *testing.T) {
	full := buildDeliverSMBody("+27830001234", "GATEWAY", 0x00, []byte("Hello"))
	// Truncate at various points — should all return -1.
	truncPoints := []int{0, 1, 3, 5, 10, 15, len(full) - 2, len(full) - 1}
	for _, n := range truncPoints {
		if n > len(full) {
			continue
		}
		got := MandatoryBodyLen(CmdSubmitSM, full[:n])
		if got != -1 {
			t.Errorf("MandatoryBodyLen(submit_sm, truncated at %d) = %d, want -1", n, got)
		}
	}
}

func TestMandatoryBodyLen_TruncatedBind(t *testing.T) {
	full := EncodeBindTransceiver("user", "pass", "type")
	// Truncate before the end.
	for _, n := range []int{0, 1, 5, len(full) - 2, len(full) - 1} {
		if n > len(full) {
			continue
		}
		got := MandatoryBodyLen(CmdBindTransceiver, full[:n])
		if got != -1 {
			t.Errorf("MandatoryBodyLen(bind_transceiver, truncated at %d) = %d, want -1", n, got)
		}
	}
}

func TestMandatoryBodyLen_EmptySubmitSMResp(t *testing.T) {
	// Empty body for submit_sm_resp — no mandatory fields.
	got := MandatoryBodyLen(CmdSubmitSMResp, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(submit_sm_resp, nil body) = %d, want 0", got)
	}
}

// =============================================================================
// ExtractTLVs tests
// =============================================================================

func TestExtractTLVs_DeliverSMWithTLVs(t *testing.T) {
	// Build a deliver_sm body with known mandatory fields.
	mandatoryBody := buildDeliverSMBody("+27830001234", "GATEWAY", 0x04, []byte("test"))

	// Append TLV bytes: receipted_message_id (0x001E) = "MSG-42"
	tlvs := make(TLVSet)
	tlvs.SetString(TagReceiptedMessageID, "MSG-42")
	tlvBytes := tlvs.Encode()

	body := make([]byte, len(mandatoryBody)+len(tlvBytes))
	copy(body, mandatoryBody)
	copy(body[len(mandatoryBody):], tlvBytes)

	pdu := &PDU{
		CommandID: CmdDeliverSM,
		Body:      body,
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}
	if extracted == nil {
		t.Fatal("ExtractTLVs() returned nil TLVSet")
	}

	val, ok := extracted.GetString(TagReceiptedMessageID)
	if !ok {
		t.Fatal("TagReceiptedMessageID not found in extracted TLVs")
	}
	if val != "MSG-42" {
		t.Errorf("TagReceiptedMessageID = %q, want %q", val, "MSG-42")
	}
}

func TestExtractTLVs_DeliverSMMultipleTLVs(t *testing.T) {
	mandatoryBody := buildDeliverSMBody("+1234", "+5678", 0x04, []byte("dlr"))

	tlvs := make(TLVSet)
	tlvs.SetString(TagReceiptedMessageID, "ID-99")
	tlvs.SetUint8(TagMessageState, MsgStateDelivered)
	tlvBytes := tlvs.Encode()

	body := make([]byte, len(mandatoryBody)+len(tlvBytes))
	copy(body, mandatoryBody)
	copy(body[len(mandatoryBody):], tlvBytes)

	pdu := &PDU{
		CommandID: CmdDeliverSM,
		Body:      body,
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}

	msgID, ok := extracted.GetString(TagReceiptedMessageID)
	if !ok || msgID != "ID-99" {
		t.Errorf("receipted_message_id = %q (ok=%v), want %q", msgID, ok, "ID-99")
	}

	state, ok := extracted.GetUint8(TagMessageState)
	if !ok || state != MsgStateDelivered {
		t.Errorf("message_state = %d (ok=%v), want %d", state, ok, MsgStateDelivered)
	}
}

func TestExtractTLVs_SubmitSMNoTLVs(t *testing.T) {
	body := buildDeliverSMBody("SRC", "DST", 0x00, []byte("Hello"))

	pdu := &PDU{
		CommandID: CmdSubmitSM,
		Body:      body,
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}
	if extracted == nil {
		t.Fatal("ExtractTLVs() returned nil TLVSet for known command")
	}
	if len(extracted) != 0 {
		t.Errorf("ExtractTLVs() returned %d entries, want 0", len(extracted))
	}
}

func TestExtractTLVs_UnknownCommand(t *testing.T) {
	pdu := &PDU{
		CommandID: 0xDEADBEEF,
		Body:      []byte{0x01, 0x02, 0x03},
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}
	if extracted != nil {
		t.Errorf("ExtractTLVs() returned non-nil TLVSet for unknown command: %v", extracted)
	}
}

func TestExtractTLVs_SubmitSMRespWithTLVs(t *testing.T) {
	// submit_sm_resp body: message_id C-string + optional TLVs.
	msgIDBytes := []byte("GW-123\x00")

	// Append a congestion_state TLV (0x0428) = 50
	tlvBuf := make([]byte, 5)
	binary.BigEndian.PutUint16(tlvBuf[0:2], uint16(TagCongestionState))
	binary.BigEndian.PutUint16(tlvBuf[2:4], 1)
	tlvBuf[4] = 50

	body := make([]byte, len(msgIDBytes)+len(tlvBuf))
	copy(body, msgIDBytes)
	copy(body[len(msgIDBytes):], tlvBuf)

	pdu := &PDU{
		CommandID: CmdSubmitSMResp,
		Body:      body,
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}

	val, ok := extracted.GetUint8(TagCongestionState)
	if !ok || val != 50 {
		t.Errorf("congestion_state = %d (ok=%v), want 50", val, ok)
	}
}

func TestExtractTLVs_EnquireLinkNoBody(t *testing.T) {
	pdu := &PDU{
		CommandID: CmdEnquireLink,
		Body:      nil,
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}
	if extracted == nil {
		t.Fatal("ExtractTLVs() returned nil for enquire_link")
	}
	if len(extracted) != 0 {
		t.Errorf("ExtractTLVs() returned %d entries, want 0", len(extracted))
	}
}

func TestExtractTLVs_MalformedTLV(t *testing.T) {
	mandatoryBody := buildDeliverSMBody("S", "D", 0x00, []byte("hi"))

	// Append truncated TLV header (only 2 bytes instead of 4).
	body := make([]byte, len(mandatoryBody)+2)
	copy(body, mandatoryBody)
	body[len(mandatoryBody)] = 0x00
	body[len(mandatoryBody)+1] = 0x1E

	pdu := &PDU{
		CommandID: CmdDeliverSM,
		Body:      body,
	}

	_, err := ExtractTLVs(pdu)
	if err == nil {
		t.Fatal("ExtractTLVs() expected error for truncated TLV, got nil")
	}
}
