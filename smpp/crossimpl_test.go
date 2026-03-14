package smpp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// =============================================================================
// Cross-implementation test vectors
// =============================================================================
//
// These tests decode known-good PDU byte sequences from other open-source SMPP
// implementations. If our decoder produces the same fields from the same bytes,
// we know we are interoperable.
//
// IMPORTANT: Do NOT modify expected values to make tests pass — fix the
// implementation instead. A mismatch means we have a bug.

// =============================================================================
// Section 1: Cloudhopper PDU Vectors (Apache 2.0)
// =============================================================================
//
// Test vectors from cloudhopper-smpp (github.com/fizzed/cloudhopper-smpp)
// Apache 2.0 license. These are wire-format bytes from their encoder/decoder
// tests.

func hexMustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q) error: %v", s, err)
	}
	return b
}

func TestCloudhopper_BindTransceiver(t *testing.T) {
	data := hexMustDecode(t, "00000023000000090000000000039951414c4c5f545700414c4c5f5457000034010200")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdBindTransceiver {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (bind_transceiver)", pdu.CommandID, CmdBindTransceiver)
	}
	if pdu.CommandStatus != StatusOK {
		t.Errorf("CommandStatus = 0x%08X, want 0x%08X", pdu.CommandStatus, StatusOK)
	}
	if pdu.SequenceNumber != 235857 {
		t.Errorf("SequenceNumber = %d, want 235857", pdu.SequenceNumber)
	}

	// Parse the bind body: system_id(C) + password(C) + system_type(C) +
	// interface_version(1) + addr_ton(1) + addr_npi(1) + address_range(C)
	body := pdu.Body
	offset := 0

	systemID, offset := ReadCString(body, offset)
	if systemID != "ALL_TW" {
		t.Errorf("system_id = %q, want %q", systemID, "ALL_TW")
	}

	password, offset := ReadCString(body, offset)
	if password != "ALL_TW" {
		t.Errorf("password = %q, want %q", password, "ALL_TW")
	}

	// system_type
	_, offset = ReadCString(body, offset)

	if offset >= len(body) {
		t.Fatal("body too short for interface_version")
	}
	interfaceVersion := body[offset]
	offset++
	if interfaceVersion != 0x34 {
		t.Errorf("interface_version = 0x%02X, want 0x34", interfaceVersion)
	}

	if offset >= len(body) {
		t.Fatal("body too short for addr_ton")
	}
	addrTON := body[offset]
	offset++
	if addrTON != 0x01 {
		t.Errorf("addr_ton = 0x%02X, want 0x01", addrTON)
	}

	if offset >= len(body) {
		t.Fatal("body too short for addr_npi")
	}
	addrNPI := body[offset]
	if addrNPI != 0x02 {
		t.Errorf("addr_npi = 0x%02X, want 0x02", addrNPI)
	}
}

func TestCloudhopper_BindTransceiverResp_WithTLV(t *testing.T) {
	data := hexMustDecode(t, "0000001d800000090000000000039943536d7363204757000210000134")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdBindTransceiverResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdBindTransceiverResp)
	}
	if pdu.CommandStatus != StatusOK {
		t.Errorf("CommandStatus = 0x%08X, want 0", pdu.CommandStatus)
	}
	if pdu.SequenceNumber != 235843 {
		t.Errorf("SequenceNumber = %d, want 235843", pdu.SequenceNumber)
	}

	systemID, tlvs := ParseBindResp(CmdBindTransceiverResp, pdu.Body)
	if systemID != "Smsc GW" {
		t.Errorf("system_id = %q, want %q", systemID, "Smsc GW")
	}

	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}
	scVersion, ok := tlvs.GetUint8(TagSCInterfaceVersion)
	if !ok {
		t.Fatal("sc_interface_version TLV not found")
	}
	if scVersion != 0x34 {
		t.Errorf("sc_interface_version = 0x%02X, want 0x34", scVersion)
	}
}

func TestCloudhopper_BindTransmitterResp_WithTLV(t *testing.T) {
	data := hexMustDecode(t, "0000001d80000002000000000003995f54574954544552000210000134")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdBindTransmitterResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdBindTransmitterResp)
	}

	systemID, tlvs := ParseBindResp(CmdBindTransmitterResp, pdu.Body)
	if systemID != "TWITTER" {
		t.Errorf("system_id = %q, want %q", systemID, "TWITTER")
	}

	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}
	scVersion, ok := tlvs.GetUint8(TagSCInterfaceVersion)
	if !ok {
		t.Fatal("sc_interface_version TLV not found")
	}
	if scVersion != 0x34 {
		t.Errorf("sc_interface_version = 0x%02X, want 0x34", scVersion)
	}
}

func TestCloudhopper_BindReceiverResp_WithTLV(t *testing.T) {
	data := hexMustDecode(t, "0000001d80000001000000000003996274776974746572000210000134")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdBindReceiverResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdBindReceiverResp)
	}

	systemID, tlvs := ParseBindResp(CmdBindReceiverResp, pdu.Body)
	if systemID != "twitter" {
		t.Errorf("system_id = %q, want %q", systemID, "twitter")
	}

	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}
	scVersion, ok := tlvs.GetUint8(TagSCInterfaceVersion)
	if !ok {
		t.Fatal("sc_interface_version TLV not found")
	}
	if scVersion != 0x34 {
		t.Errorf("sc_interface_version = 0x%02X, want 0x34", scVersion)
	}
}

func TestCloudhopper_SubmitSM(t *testing.T) {
	data := hexMustDecode(t, "00000039000000040000000000004FE80001013430343034000101343439353133363139323000000000000001000000084024232125262F3A")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdSubmitSM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (submit_sm)", pdu.CommandID, CmdSubmitSM)
	}

	// submit_sm and deliver_sm share the same mandatory field layout, so we
	// can use ParseDeliverSM to extract the addresses and message.
	sourceAddr, destAddr, esmClass, shortMessage := ParseDeliverSM(pdu.Body)
	if sourceAddr != "40404" {
		t.Errorf("source_addr = %q, want %q", sourceAddr, "40404")
	}
	if destAddr != "44951361920" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "44951361920")
	}
	if esmClass != 0x00 {
		t.Errorf("esm_class = 0x%02X, want 0x00", esmClass)
	}

	wantMsg := []byte{0x40, 0x24, 0x23, 0x21, 0x25, 0x26, 0x2F, 0x3A}
	if !bytes.Equal(shortMessage, wantMsg) {
		t.Errorf("short_message = %x, want %x", shortMessage, wantMsg)
	}
	if string(shortMessage) != `@$#!%&/:` {
		t.Errorf("short_message text = %q, want %q", string(shortMessage), `@$#!%&/:`)
	}

	// Verify registered_delivery=0x01 by parsing the body manually.
	// After esm_class, we have: protocol_id(1), priority_flag(1),
	// schedule_delivery_time(C), validity_period(C), registered_delivery(1)
	offset := 0
	// service_type
	_, offset = ReadCString(pdu.Body, offset)
	// source_addr_ton + source_addr_npi
	offset += 2
	// source_addr
	_, offset = ReadCString(pdu.Body, offset)
	// dest_addr_ton + dest_addr_npi
	offset += 2
	// dest_addr
	_, offset = ReadCString(pdu.Body, offset)
	// esm_class
	offset++
	// protocol_id
	offset++
	// priority_flag
	offset++
	// schedule_delivery_time
	_, offset = ReadCString(pdu.Body, offset)
	// validity_period
	_, offset = ReadCString(pdu.Body, offset)
	// registered_delivery
	if offset < len(pdu.Body) {
		regDel := pdu.Body[offset]
		if regDel != 0x01 {
			t.Errorf("registered_delivery = 0x%02X, want 0x01", regDel)
		}
	} else {
		t.Error("body too short for registered_delivery")
	}
}

func TestCloudhopper_SubmitSMResp(t *testing.T) {
	data := hexMustDecode(t, "0000001c80000004000000000a342ee1393432353834333135393400")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdSubmitSMResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdSubmitSMResp)
	}
	if pdu.CommandStatus != StatusOK {
		t.Errorf("CommandStatus = 0x%08X, want 0", pdu.CommandStatus)
	}
	if pdu.SequenceNumber != 171192033 {
		t.Errorf("SequenceNumber = %d, want 171192033", pdu.SequenceNumber)
	}

	messageID := ParseSubmitSMResp(pdu.Body)
	if messageID != "94258431594" {
		t.Errorf("message_id = %q, want %q", messageID, "94258431594")
	}
}

func TestCloudhopper_SubmitSMResp_EmptyMessageID(t *testing.T) {
	data := hexMustDecode(t, "0000001180000004000000000a342ee100")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdSubmitSMResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdSubmitSMResp)
	}

	messageID := ParseSubmitSMResp(pdu.Body)
	if messageID != "" {
		t.Errorf("message_id = %q, want empty", messageID)
	}
}

func TestCloudhopper_SubmitSMResp_Error_NoBody(t *testing.T) {
	data := hexMustDecode(t, "0000001080000004000000300a342ee1")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdSubmitSMResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdSubmitSMResp)
	}
	if pdu.CommandStatus != 0x30 {
		t.Errorf("CommandStatus = 0x%08X, want 0x00000030", pdu.CommandStatus)
	}
	if len(pdu.Body) != 0 {
		t.Errorf("body length = %d, want 0 (header-only PDU)", len(pdu.Body))
	}
}

func TestCloudhopper_DeliverSM_WithTLVs(t *testing.T) {
	data := hexMustDecode(t, "000000400000000500000000000000030002013837363534333231000409343034303400000000000000000000084024232125262F3A000E0001010006000101")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdDeliverSM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (deliver_sm)", pdu.CommandID, CmdDeliverSM)
	}
	if pdu.SequenceNumber != 3 {
		t.Errorf("SequenceNumber = %d, want 3", pdu.SequenceNumber)
	}

	sourceAddr, destAddr, esmClass, shortMessage := ParseDeliverSM(pdu.Body)
	if sourceAddr != "87654321" {
		t.Errorf("source_addr = %q, want %q", sourceAddr, "87654321")
	}
	if destAddr != "40404" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "40404")
	}
	if esmClass != 0x00 {
		t.Errorf("esm_class = 0x%02X, want 0x00", esmClass)
	}

	wantMsg := []byte{0x40, 0x24, 0x23, 0x21, 0x25, 0x26, 0x2F, 0x3A}
	if !bytes.Equal(shortMessage, wantMsg) {
		t.Errorf("short_message = %x, want %x", shortMessage, wantMsg)
	}

	// Verify source_addr TON and NPI by reading the body directly.
	offset := 0
	// service_type
	_, offset = ReadCString(pdu.Body, offset)
	srcTON := pdu.Body[offset]
	srcNPI := pdu.Body[offset+1]
	if srcTON != 0x02 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x02", srcTON)
	}
	if srcNPI != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", srcNPI)
	}
	offset += 2
	_, offset = ReadCString(pdu.Body, offset)
	dstTON := pdu.Body[offset]
	dstNPI := pdu.Body[offset+1]
	if dstTON != 0x04 {
		t.Errorf("dest_addr_ton = 0x%02X, want 0x04", dstTON)
	}
	if dstNPI != 0x09 {
		t.Errorf("dest_addr_npi = 0x%02X, want 0x09", dstNPI)
	}

	// Extract TLVs using ExtractTLVs.
	tlvs, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs error: %v", err)
	}
	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}

	// TLV tag 0x000E = TagSourceNetworkType, value = 0x01
	srcNet, ok := tlvs.GetUint8(TagSourceNetworkType)
	if !ok {
		t.Error("source_network_type TLV (0x000E) not found")
	} else if srcNet != 0x01 {
		t.Errorf("source_network_type = 0x%02X, want 0x01", srcNet)
	}

	// TLV tag 0x0006 = TagDestNetworkType, value = 0x01
	dstNet, ok := tlvs.GetUint8(TagDestNetworkType)
	if !ok {
		t.Error("dest_network_type TLV (0x0006) not found")
	} else if dstNet != 0x01 {
		t.Errorf("dest_network_type = 0x%02X, want 0x01", dstNet)
	}
}

func TestCloudhopper_DeliverSM_DLR_WithTLVs(t *testing.T) {
	data := hexMustDecode(t, "000000BA00000005000000000000000200010134343935313336313932300001013430343034000400000000000000006E69643A30303539313133393738207375623A30303120646C7672643A303031207375626D697420646174653A3130303231303137333020646F6E6520646174653A3130303231303137333120737461743A44454C49565244206572723A30303020746578743A4024232125262F3A000E0001010006000101001E000833383630316661000427000102")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdDeliverSM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdDeliverSM)
	}

	sourceAddr, destAddr, esmClass, shortMessage := ParseDeliverSM(pdu.Body)
	if sourceAddr != "44951361920" {
		t.Errorf("source_addr = %q, want %q", sourceAddr, "44951361920")
	}
	if destAddr != "40404" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "40404")
	}
	if esmClass != 0x04 {
		t.Errorf("esm_class = 0x%02X, want 0x04 (DLR)", esmClass)
	}
	if !IsDLR(esmClass) {
		t.Error("IsDLR(0x04) = false, want true")
	}

	// Parse the DLR receipt text.
	dlrText := string(shortMessage)
	if len(dlrText) < 20 {
		t.Fatalf("short_message too short: %q", dlrText)
	}
	if dlrText[:16] != "id:0059113978 su" {
		t.Errorf("short_message starts with %q, want prefix 'id:0059113978 su'", dlrText[:16])
	}

	receipt := ParseDLRReceipt(dlrText)
	if receipt == nil {
		t.Fatal("ParseDLRReceipt returned nil")
	}
	if receipt.MessageID != "0059113978" {
		t.Errorf("DLR MessageID = %q, want %q", receipt.MessageID, "0059113978")
	}
	if receipt.Status != "DELIVRD" {
		t.Errorf("DLR Status = %q, want %q", receipt.Status, "DELIVRD")
	}
	if receipt.ErrorCode != "000" {
		t.Errorf("DLR ErrorCode = %q, want %q", receipt.ErrorCode, "000")
	}

	// Extract TLVs.
	tlvs, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs error: %v", err)
	}
	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}

	// receipted_message_id (0x001E) = "38601fa"
	receiptedID, ok := tlvs.GetString(TagReceiptedMessageID)
	if !ok {
		t.Error("receipted_message_id TLV not found")
	} else if receiptedID != "38601fa" {
		t.Errorf("receipted_message_id = %q, want %q", receiptedID, "38601fa")
	}

	// message_state (0x0427) = DELIVERED (0x02)
	msgState, ok := tlvs.GetUint8(TagMessageState)
	if !ok {
		t.Error("message_state TLV not found")
	} else if msgState != MsgStateDelivered {
		t.Errorf("message_state = %d, want %d (DELIVERED)", msgState, MsgStateDelivered)
	}
}

func TestCloudhopper_DeliverSM_MessagePayloadTLV(t *testing.T) {
	data := hexMustDecode(t, "000000640000000500000000000547EB0002013434393531333631393200040934303430340000000000000000000000000E000101000600010104240026404D616964656E6D616E363634207761732069742073617070793F2026526F6D616E7469633F")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdDeliverSM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdDeliverSM)
	}

	_, _, _, shortMessage := ParseDeliverSM(pdu.Body)
	if shortMessage != nil {
		t.Errorf("short_message should be nil (sm_length=0), got %d bytes", len(shortMessage))
	}

	// Extract TLVs and check message_payload.
	tlvs, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs error: %v", err)
	}
	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}

	payload, ok := tlvs.GetString(TagMessagePayload)
	if !ok {
		t.Fatal("message_payload TLV (0x0424) not found")
	}
	wantPayload := "@Maidenman664 was it sappy? &Romantic?"
	if payload != wantPayload {
		t.Errorf("message_payload = %q, want %q", payload, wantPayload)
	}
}

func TestCloudhopper_EnquireLink(t *testing.T) {
	data := hexMustDecode(t, "0000001000000015000000000a342ee7")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdEnquireLink {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (enquire_link)", pdu.CommandID, CmdEnquireLink)
	}
	if pdu.SequenceNumber != 171192039 {
		t.Errorf("SequenceNumber = %d, want 171192039", pdu.SequenceNumber)
	}
	if len(pdu.Body) != 0 {
		t.Errorf("body length = %d, want 0", len(pdu.Body))
	}
}

func TestCloudhopper_GenericNack(t *testing.T) {
	data := hexMustDecode(t, "00000010800000000000000100082a77")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdGenericNack {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (generic_nack)", pdu.CommandID, CmdGenericNack)
	}
	if pdu.CommandStatus != StatusInvMsgLen {
		t.Errorf("CommandStatus = 0x%08X, want 0x%08X (ESME_RINVMSGLEN)", pdu.CommandStatus, StatusInvMsgLen)
	}
	if pdu.SequenceNumber != 535159 {
		t.Errorf("SequenceNumber = %d, want 535159", pdu.SequenceNumber)
	}
}

func TestCloudhopper_QuerySM(t *testing.T) {
	data := hexMustDecode(t, "0000001E000000030000000000004FE83132333435000101343034303400")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdQuerySM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (query_sm)", pdu.CommandID, CmdQuerySM)
	}

	// Parse query_sm body: message_id(C) + source_addr_ton(1) +
	// source_addr_npi(1) + source_addr(C)
	body := pdu.Body
	offset := 0

	messageID, offset := ReadCString(body, offset)
	if messageID != "12345" {
		t.Errorf("message_id = %q, want %q", messageID, "12345")
	}

	if offset+2 > len(body) {
		t.Fatal("body too short for source_addr_ton/npi")
	}
	srcTON := body[offset]
	srcNPI := body[offset+1]
	offset += 2
	if srcTON != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", srcTON)
	}
	if srcNPI != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", srcNPI)
	}

	sourceAddr, _ := ReadCString(body, offset)
	if sourceAddr != "40404" {
		t.Errorf("source_addr = %q, want %q", sourceAddr, "40404")
	}
}

func TestCloudhopper_QuerySMResp(t *testing.T) {
	data := hexMustDecode(t, "00000019800000030000000000004FE8313233343500000600")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdQuerySMResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdQuerySMResp)
	}

	messageID, finalDate, msgState, errCode := ParseQuerySMResp(pdu.Body)
	if messageID != "12345" {
		t.Errorf("message_id = %q, want %q", messageID, "12345")
	}
	if finalDate != "" {
		t.Errorf("final_date = %q, want empty", finalDate)
	}
	if msgState != MsgStateAccepted {
		t.Errorf("message_state = %d, want %d (ACCEPTED)", msgState, MsgStateAccepted)
	}
	if errCode != 0x00 {
		t.Errorf("error_code = 0x%02X, want 0x00", errCode)
	}
}

func TestCloudhopper_CancelSM(t *testing.T) {
	data := hexMustDecode(t, "0000002D000000080000000000004FE80031323334350001013430343034000101343439353133363139323000")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdCancelSM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (cancel_sm)", pdu.CommandID, CmdCancelSM)
	}

	// Parse cancel_sm body: service_type(C) + message_id(C) +
	// source_addr_ton(1) + source_addr_npi(1) + source_addr(C) +
	// dest_addr_ton(1) + dest_addr_npi(1) + destination_addr(C)
	body := pdu.Body
	offset := 0

	serviceType, offset := ReadCString(body, offset)
	if serviceType != "" {
		t.Errorf("service_type = %q, want empty", serviceType)
	}

	messageID, offset := ReadCString(body, offset)
	if messageID != "12345" {
		t.Errorf("message_id = %q, want %q", messageID, "12345")
	}

	offset += 2 // source_addr_ton + source_addr_npi
	sourceAddr, offset := ReadCString(body, offset)
	if sourceAddr != "40404" {
		t.Errorf("source_addr = %q, want %q", sourceAddr, "40404")
	}

	offset += 2 // dest_addr_ton + dest_addr_npi
	destAddr, _ := ReadCString(body, offset)
	if destAddr != "44951361920" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "44951361920")
	}
}

func TestCloudhopper_DataSM(t *testing.T) {
	data := hexMustDecode(t, "000000300000010300000000000000000001013535353237313030303000000139363935000001000424000454657374")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdDataSM {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (data_sm)", pdu.CommandID, CmdDataSM)
	}

	// Parse data_sm body to get addresses.
	body := pdu.Body
	offset := 0

	// service_type
	_, offset = ReadCString(body, offset)
	// source_addr_ton + source_addr_npi
	offset += 2
	sourceAddr, offset := ReadCString(body, offset)
	if sourceAddr != "5552710000" {
		t.Errorf("source_addr = %q, want %q", sourceAddr, "5552710000")
	}

	// dest_addr_ton + dest_addr_npi
	offset += 2
	destAddr, _ := ReadCString(body, offset)
	if destAddr != "9695" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "9695")
	}

	// Extract TLVs — data_sm carries the message in message_payload TLV.
	tlvs, err := ExtractTLVs(pdu)
	if err != nil {
		t.Fatalf("ExtractTLVs error: %v", err)
	}
	if tlvs == nil {
		t.Fatal("expected TLVs, got nil")
	}

	payload, ok := tlvs.GetString(TagMessagePayload)
	if !ok {
		t.Fatal("message_payload TLV (0x0424) not found")
	}
	if payload != "Test" {
		t.Errorf("message_payload = %q, want %q", payload, "Test")
	}
}

func TestCloudhopper_DataSMResp(t *testing.T) {
	data := hexMustDecode(t, "0000001c800001030000000000116ac7393432353834333135393400")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdDataSMResp {
		t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, CmdDataSMResp)
	}

	messageID := ParseDataSMResp(pdu.Body)
	if messageID != "94258431594" {
		t.Errorf("message_id = %q, want %q", messageID, "94258431594")
	}
}

func TestCloudhopper_AlertNotification(t *testing.T) {
	data := hexMustDecode(t, "00000025000001020000000200004FE8010135353532373130303030000101343034303400")

	pdu, err := DecodePDU(data)
	if err != nil {
		t.Fatalf("DecodePDU error: %v", err)
	}

	if pdu.CommandID != CmdAlertNotification {
		t.Errorf("CommandID = 0x%08X, want 0x%08X (alert_notification)", pdu.CommandID, CmdAlertNotification)
	}
	if pdu.CommandStatus != 0x00000002 {
		t.Errorf("CommandStatus = 0x%08X, want 0x00000002", pdu.CommandStatus)
	}

	srcTON, srcNPI, srcAddr, esmeTON, esmeNPI, esmeAddr := ParseAlertNotification(pdu.Body)
	if srcTON != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", srcTON)
	}
	if srcNPI != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", srcNPI)
	}
	if srcAddr != "5552710000" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "5552710000")
	}
	if esmeTON != 0x01 {
		t.Errorf("esme_addr_ton = 0x%02X, want 0x01", esmeTON)
	}
	if esmeNPI != 0x01 {
		t.Errorf("esme_addr_npi = 0x%02X, want 0x01", esmeNPI)
	}
	if esmeAddr != "40404" {
		t.Errorf("esme_addr = %q, want %q", esmeAddr, "40404")
	}
}

// =============================================================================
// Section 2: GSM 7-bit Packing Vectors (CursedHardware, MIT)
// =============================================================================
//
// GSM 7-bit packed encoding vectors from CursedHardware/go-smpp (MIT license).
// These verify our packed output matches another implementation byte-for-byte.

func TestCursedHardware_GSM7Packing(t *testing.T) {
	tests := []struct {
		input       string
		wantHex     string
		wantSeptets int
	}{
		{"", "", 0},
		{"1", "31", 1},
		{"12", "3119", 2},
		{"123", "31D90C", 3},
		{"1234", "31D98C06", 4},
		{"12345", "31D98C5603", 5},
		{"123456", "31D98C56B301", 6},
		{"1234567", "31D98C56B3DD1A", 7},
		{"12345678", "31D98C56B3DD70", 8},
		{"123456789", "31D98C56B3DD7039", 9},
	}

	for _, tt := range tests {
		t.Run("encode_"+tt.input, func(t *testing.T) {
			if tt.input == "" {
				// EncodeGSM7("") returns ([], 0, nil) — empty packed bytes.
				packed, septets, err := EncodeGSM7(tt.input)
				if err != nil {
					t.Fatalf("EncodeGSM7(%q) error: %v", tt.input, err)
				}
				if septets != 0 {
					t.Errorf("septets = %d, want 0", septets)
				}
				if len(packed) != 0 {
					t.Errorf("packed = %x, want empty", packed)
				}
				return
			}

			packed, septets, err := EncodeGSM7(tt.input)
			if err != nil {
				t.Fatalf("EncodeGSM7(%q) error: %v", tt.input, err)
			}
			if septets != tt.wantSeptets {
				t.Errorf("septets = %d, want %d", septets, tt.wantSeptets)
			}

			gotHex := hex.EncodeToString(packed)
			wantHexLower := toLower(tt.wantHex)
			if gotHex != wantHexLower {
				t.Errorf("packed = %s, want %s", gotHex, wantHexLower)
			}
		})
	}

	// Decode test: verify each packed hex decodes back to the original text.
	for _, tt := range tests {
		if tt.input == "" {
			continue
		}
		t.Run("decode_"+tt.input, func(t *testing.T) {
			packed := hexMustDecode(t, tt.wantHex)
			decoded, err := DecodeGSM7(packed, tt.wantSeptets)
			if err != nil {
				t.Fatalf("DecodeGSM7 error: %v", err)
			}
			if decoded != tt.input {
				t.Errorf("decoded = %q, want %q", decoded, tt.input)
			}
		})
	}
}

func TestCursedHardware_GSM7ExtensionChars(t *testing.T) {
	// Extension characters: ^{}\\[~]|€
	// Each costs 2 septets (escape 0x1B + code).
	input := "^{}\\[~]|€"
	wantHex := "1bca06b5496d5e1bdea6b7f16d809b32"

	packed, septets, err := EncodeGSM7(input)
	if err != nil {
		t.Fatalf("EncodeGSM7(%q) error: %v", input, err)
	}

	// 9 extension chars, each 2 septets = 18 septets.
	if septets != 18 {
		t.Errorf("septets = %d, want 18", septets)
	}

	gotHex := hex.EncodeToString(packed)
	if gotHex != wantHex {
		t.Errorf("packed = %s, want %s", gotHex, wantHex)
	}

	// Verify decode round-trip.
	decoded, err := DecodeGSM7(packed, septets)
	if err != nil {
		t.Fatalf("DecodeGSM7 error: %v", err)
	}
	if decoded != input {
		t.Errorf("decoded = %q, want %q", decoded, input)
	}
}

// =============================================================================
// Section 3: DLR Receipt Edge Cases (Cloudhopper)
// =============================================================================
//
// Additional DLR receipt strings from cloudhopper-smpp not already covered
// in handler_test.go.

func TestCloudhopper_DLR_OutOfOrderFields(t *testing.T) {
	// Fields in non-standard order, "done date" and "submit date" with
	// space-separated keys (common in real-world SMSCs).
	text := "sub:001 id:74e02ee1 err:000 dlvrd:001 done date:110206193110 submit date:110206193041 text: stat:DELIVRD"
	receipt := ParseDLRReceipt(text)
	if receipt == nil {
		t.Fatal("ParseDLRReceipt returned nil")
	}
	if receipt.MessageID != "74e02ee1" {
		t.Errorf("MessageID = %q, want %q", receipt.MessageID, "74e02ee1")
	}
	if receipt.Status != "DELIVRD" {
		t.Errorf("Status = %q, want %q", receipt.Status, "DELIVRD")
	}
	if receipt.ErrorCode != "000" {
		t.Errorf("ErrorCode = %q, want %q", receipt.ErrorCode, "000")
	}
}

func TestCloudhopper_DLR_MissingSubDlvrd(t *testing.T) {
	// Missing sub: and dlvrd: fields — should still parse id, stat, err.
	text := "id:2E179B310EDE971B2760C72B7F026E1B submit date:20110314181534 done date:20110314181741 stat:DELIVRD err:0"
	receipt := ParseDLRReceipt(text)
	if receipt == nil {
		t.Fatal("ParseDLRReceipt returned nil")
	}
	if receipt.MessageID != "2E179B310EDE971B2760C72B7F026E1B" {
		t.Errorf("MessageID = %q, want %q", receipt.MessageID, "2E179B310EDE971B2760C72B7F026E1B")
	}
	if receipt.Status != "DELIVRD" {
		t.Errorf("Status = %q, want %q", receipt.Status, "DELIVRD")
	}
	if receipt.ErrorCode != "0" {
		t.Errorf("ErrorCode = %q, want %q", receipt.ErrorCode, "0")
	}
}

func TestCloudhopper_DLR_NonReceipt(t *testing.T) {
	// Text that looks like it might be a DLR (contains "submit date:")
	// but lacks the id: prefix. Should return nil.
	text := "submit date:110206193041"
	receipt := ParseDLRReceipt(text)
	if receipt != nil {
		t.Errorf("expected nil for non-receipt text, got %+v", receipt)
	}
}

func TestCloudhopper_DLR_CapitalText(t *testing.T) {
	// "Text:" with capital T (some Ericsson SMSCs).
	text := "id:4 sub:001 dlvrd:001 submit date:1006020051 done date:1006020051 stat:DELIVRD err:000 Text:Hello"
	receipt := ParseDLRReceipt(text)
	if receipt == nil {
		t.Fatal("ParseDLRReceipt returned nil")
	}
	if receipt.MessageID != "4" {
		t.Errorf("MessageID = %q, want %q", receipt.MessageID, "4")
	}
	if receipt.Status != "DELIVRD" {
		t.Errorf("Status = %q, want %q", receipt.Status, "DELIVRD")
	}
	if receipt.ErrorCode != "000" {
		t.Errorf("ErrorCode = %q, want %q", receipt.ErrorCode, "000")
	}
}

// =============================================================================
// Helpers
// =============================================================================

// toLower converts a hex string to lowercase for comparison.
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + 32
		}
	}
	return string(b)
}
