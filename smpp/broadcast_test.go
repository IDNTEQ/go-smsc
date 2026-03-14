package smpp

import (
	"bytes"
	"testing"
)

// =============================================================================
// broadcast_sm encode/parse roundtrip
// =============================================================================

func TestEncodeBroadcastSM_Roundtrip(t *testing.T) {
	body := EncodeBroadcastSM("CBS", 0x05, 0x00, "BCAST",
		"MSG-B01", 0x01, "210601120000000+", "210701120000000+",
		0x00, 0x01, 0x00)

	if len(body) == 0 {
		t.Fatal("EncodeBroadcastSM() returned empty body")
	}

	offset := 0

	// service_type
	svcType, offset := readCString(body, offset)
	if svcType != "CBS" {
		t.Errorf("service_type = %q, want %q", svcType, "CBS")
	}

	// source_addr_ton
	if body[offset] != 0x05 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x05", body[offset])
	}
	offset++

	// source_addr_npi
	if body[offset] != 0x00 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x00", body[offset])
	}
	offset++

	// source_addr
	srcAddr, offset := readCString(body, offset)
	if srcAddr != "BCAST" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "BCAST")
	}

	// message_id
	msgID, offset := readCString(body, offset)
	if msgID != "MSG-B01" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-B01")
	}

	// priority_flag
	if body[offset] != 0x01 {
		t.Errorf("priority_flag = 0x%02X, want 0x01", body[offset])
	}
	offset++

	// schedule_delivery_time
	sdt, offset := readCString(body, offset)
	if sdt != "210601120000000+" {
		t.Errorf("schedule_delivery_time = %q, want %q", sdt, "210601120000000+")
	}

	// validity_period
	vp, offset := readCString(body, offset)
	if vp != "210701120000000+" {
		t.Errorf("validity_period = %q, want %q", vp, "210701120000000+")
	}

	// replace_if_present
	if body[offset] != 0x00 {
		t.Errorf("replace_if_present = 0x%02X, want 0x00", body[offset])
	}
	offset++

	// data_coding
	if body[offset] != 0x01 {
		t.Errorf("data_coding = 0x%02X, want 0x01", body[offset])
	}
	offset++

	// sm_default_msg_id
	if body[offset] != 0x00 {
		t.Errorf("sm_default_msg_id = 0x%02X, want 0x00", body[offset])
	}
	offset++

	if offset != len(body) {
		t.Errorf("unexpected trailing bytes: consumed %d, total %d", offset, len(body))
	}
}

func TestParseBroadcastSMResp(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		wantID string
	}{
		{"normal", []byte("BCAST-001\x00"), "BCAST-001"},
		{"empty body", nil, ""},
		{"just null", []byte{0x00}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBroadcastSMResp(tt.body)
			if got != tt.wantID {
				t.Errorf("ParseBroadcastSMResp() = %q, want %q", got, tt.wantID)
			}
		})
	}
}

// =============================================================================
// query_broadcast_sm encode/parse roundtrip
// =============================================================================

func TestEncodeQueryBroadcastSM_Roundtrip(t *testing.T) {
	body := EncodeQueryBroadcastSM("MSG-B99", 0x01, 0x01, "+27830001234")

	offset := 0

	// message_id
	msgID, offset := readCString(body, offset)
	if msgID != "MSG-B99" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-B99")
	}

	// source_addr_ton
	if body[offset] != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++

	// source_addr_npi
	if body[offset] != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++

	// source_addr
	srcAddr, offset := readCString(body, offset)
	if srcAddr != "+27830001234" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "+27830001234")
	}

	if offset != len(body) {
		t.Errorf("unexpected trailing bytes: consumed %d, total %d", offset, len(body))
	}
}

func TestParseQueryBroadcastSMResp(t *testing.T) {
	// Build a query_broadcast_sm_resp body.
	var body []byte
	body = append(body, []byte("BCAST-42\x00")...) // message_id
	body = append(body, MsgStateDelivered)          // message_state

	msgID, msgState := ParseQueryBroadcastSMResp(body)
	if msgID != "BCAST-42" {
		t.Errorf("message_id = %q, want %q", msgID, "BCAST-42")
	}
	if msgState != MsgStateDelivered {
		t.Errorf("message_state = %d, want %d", msgState, MsgStateDelivered)
	}
}

func TestParseQueryBroadcastSMResp_EmptyBody(t *testing.T) {
	msgID, msgState := ParseQueryBroadcastSMResp(nil)
	if msgID != "" || msgState != 0 {
		t.Errorf("expected zero values, got %q %d", msgID, msgState)
	}
}

// =============================================================================
// cancel_broadcast_sm encode roundtrip
// =============================================================================

func TestEncodeCancelBroadcastSM_Roundtrip(t *testing.T) {
	body := EncodeCancelBroadcastSM("CBS", "MSG-B50", 0x05, 0x00, "BCAST-SRC")

	offset := 0

	// service_type
	svcType, offset := readCString(body, offset)
	if svcType != "CBS" {
		t.Errorf("service_type = %q, want %q", svcType, "CBS")
	}

	// message_id
	msgID, offset := readCString(body, offset)
	if msgID != "MSG-B50" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-B50")
	}

	// source_addr_ton
	if body[offset] != 0x05 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x05", body[offset])
	}
	offset++

	// source_addr_npi
	if body[offset] != 0x00 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x00", body[offset])
	}
	offset++

	// source_addr
	srcAddr, offset := readCString(body, offset)
	if srcAddr != "BCAST-SRC" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "BCAST-SRC")
	}

	if offset != len(body) {
		t.Errorf("unexpected trailing bytes: consumed %d, total %d", offset, len(body))
	}
}

// =============================================================================
// MandatoryBodyLen for broadcast PDU types
// =============================================================================

func TestMandatoryBodyLen_BroadcastSM(t *testing.T) {
	body := EncodeBroadcastSM("", 0x01, 0x01, "+1234",
		"MSG-1", 0x00, "", "", 0x00, 0x00, 0x00)
	got := MandatoryBodyLen(CmdBroadcastSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(broadcast_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_BroadcastSMResp(t *testing.T) {
	body := []byte("BCAST-001\x00")
	got := MandatoryBodyLen(CmdBroadcastSMResp, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(broadcast_sm_resp) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_QueryBroadcastSM(t *testing.T) {
	body := EncodeQueryBroadcastSM("MSG-1", 0x01, 0x01, "+1234")
	got := MandatoryBodyLen(CmdQueryBroadcastSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(query_broadcast_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_QueryBroadcastSMResp(t *testing.T) {
	var body []byte
	body = append(body, []byte("MSG-1\x00")...)
	body = append(body, MsgStateDelivered) // message_state

	got := MandatoryBodyLen(CmdQueryBroadcastSMResp, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(query_broadcast_sm_resp) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_CancelBroadcastSM(t *testing.T) {
	body := EncodeCancelBroadcastSM("", "MSG-1", 0x01, 0x01, "+1234")
	got := MandatoryBodyLen(CmdCancelBroadcastSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(cancel_broadcast_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_CancelBroadcastSMResp(t *testing.T) {
	got := MandatoryBodyLen(CmdCancelBroadcastSMResp, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(cancel_broadcast_sm_resp) = %d, want 0", got)
	}
}

// =============================================================================
// BroadcastSM with TLV extraction
// =============================================================================

func TestExtractTLVs_BroadcastSMResp(t *testing.T) {
	// Build a broadcast_sm_resp body with message_id + TLVs.
	mandatoryBody := []byte("BCAST-42\x00")

	tlvs := make(TLVSet)
	tlvs.SetUint32(TagBroadcastErrorStatus, 0x00000000)
	tlvBytes := tlvs.Encode()

	body := make([]byte, len(mandatoryBody)+len(tlvBytes))
	copy(body, mandatoryBody)
	copy(body[len(mandatoryBody):], tlvBytes)

	pdu := &PDU{
		CommandID: CmdBroadcastSMResp,
		Body:      body,
	}

	extracted, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs() error: %v", err)
	}
	if extracted == nil {
		t.Fatal("ExtractTLVs() returned nil TLVSet")
	}

	val, ok := extracted.GetUint32(TagBroadcastErrorStatus)
	if !ok {
		t.Fatal("TagBroadcastErrorStatus not found in extracted TLVs")
	}
	if val != 0x00000000 {
		t.Errorf("broadcast_error_status = 0x%08X, want 0x00000000", val)
	}
}

// =============================================================================
// CongestionState convenience method
// =============================================================================

func TestTLVSet_CongestionState(t *testing.T) {
	ts := make(TLVSet)

	// Not present => -1
	if got := ts.CongestionState(); got != -1 {
		t.Errorf("CongestionState() = %d, want -1 for absent tag", got)
	}

	// Set to 50
	ts.SetUint8(TagCongestionState, 50)
	if got := ts.CongestionState(); got != 50 {
		t.Errorf("CongestionState() = %d, want 50", got)
	}

	// Set to 0
	ts.SetUint8(TagCongestionState, 0)
	if got := ts.CongestionState(); got != 0 {
		t.Errorf("CongestionState() = %d, want 0", got)
	}

	// Set to 99
	ts.SetUint8(TagCongestionState, 99)
	if got := ts.CongestionState(); got != 99 {
		t.Errorf("CongestionState() = %d, want 99", got)
	}
}

// =============================================================================
// Network routing TLV convenience methods
// =============================================================================

func TestTLVSet_SourceNetworkID(t *testing.T) {
	ts := make(TLVSet)

	// Absent
	_, ok := ts.SourceNetworkID()
	if ok {
		t.Error("SourceNetworkID() ok = true, want false for absent tag")
	}

	// Present
	ts.SetString(TagSourceNetworkID, "NET-001")
	val, ok := ts.SourceNetworkID()
	if !ok {
		t.Fatal("SourceNetworkID() ok = false, want true")
	}
	if val != "NET-001" {
		t.Errorf("SourceNetworkID() = %q, want %q", val, "NET-001")
	}
}

func TestTLVSet_DestNetworkID(t *testing.T) {
	ts := make(TLVSet)

	_, ok := ts.DestNetworkID()
	if ok {
		t.Error("DestNetworkID() ok = true, want false for absent tag")
	}

	ts.SetString(TagDestNetworkID, "NET-002")
	val, ok := ts.DestNetworkID()
	if !ok {
		t.Fatal("DestNetworkID() ok = false, want true")
	}
	if val != "NET-002" {
		t.Errorf("DestNetworkID() = %q, want %q", val, "NET-002")
	}
}

func TestTLVSet_SourceNodeID(t *testing.T) {
	ts := make(TLVSet)

	_, ok := ts.SourceNodeID()
	if ok {
		t.Error("SourceNodeID() ok = true, want false for absent tag")
	}

	ts.SetString(TagSourceNodeID, "NODE-A")
	val, ok := ts.SourceNodeID()
	if !ok {
		t.Fatal("SourceNodeID() ok = false, want true")
	}
	if val != "NODE-A" {
		t.Errorf("SourceNodeID() = %q, want %q", val, "NODE-A")
	}
}

func TestTLVSet_DestNodeID(t *testing.T) {
	ts := make(TLVSet)

	_, ok := ts.DestNodeID()
	if ok {
		t.Error("DestNodeID() ok = true, want false for absent tag")
	}

	ts.SetString(TagDestNodeID, "NODE-B")
	val, ok := ts.DestNodeID()
	if !ok {
		t.Fatal("DestNodeID() ok = false, want true")
	}
	if val != "NODE-B" {
		t.Errorf("DestNodeID() = %q, want %q", val, "NODE-B")
	}
}

func TestTLVSet_BillingIdentification(t *testing.T) {
	ts := make(TLVSet)

	// Absent
	_, ok := ts.BillingIdentification()
	if ok {
		t.Error("BillingIdentification() ok = true, want false for absent tag")
	}

	// Present
	billing := []byte{0x01, 0x02, 0x03, 0x04}
	ts.SetBytes(TagBillingIdentification, billing)
	val, ok := ts.BillingIdentification()
	if !ok {
		t.Fatal("BillingIdentification() ok = false, want true")
	}
	if !bytes.Equal(val, billing) {
		t.Errorf("BillingIdentification() = %v, want %v", val, billing)
	}
}

// =============================================================================
// Registered delivery constants
// =============================================================================

func TestRegDeliveryConstants(t *testing.T) {
	if RegDeliveryNone != 0x00 {
		t.Errorf("RegDeliveryNone = 0x%02X, want 0x00", RegDeliveryNone)
	}
	if RegDeliveryBoth != 0x01 {
		t.Errorf("RegDeliveryBoth = 0x%02X, want 0x01", RegDeliveryBoth)
	}
	if RegDeliveryFailure != 0x02 {
		t.Errorf("RegDeliveryFailure = 0x%02X, want 0x02", RegDeliveryFailure)
	}
	if RegDeliverySuccess != 0x03 {
		t.Errorf("RegDeliverySuccess = 0x%02X, want 0x03", RegDeliverySuccess)
	}
}
