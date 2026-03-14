package smpp

import (
	"encoding/binary"
	"testing"
)

// =============================================================================
// query_sm encode/decode roundtrip
// =============================================================================

func TestQuerySM_Roundtrip(t *testing.T) {
	body := EncodeQuerySM("MSG-42", "+27830001234", 0x01, 0x01)

	// Parse back manually.
	offset := 0
	msgID, offset := readCString(body, offset)
	if msgID != "MSG-42" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-42")
	}
	if offset >= len(body) {
		t.Fatal("body too short for source_addr_ton")
	}
	if body[offset] != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++
	srcAddr, _ := readCString(body, offset)
	if srcAddr != "+27830001234" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "+27830001234")
	}
}

func TestParseQuerySMResp_Roundtrip(t *testing.T) {
	// Build a query_sm_resp body.
	var body []byte
	body = append(body, []byte("MSG-42\x00")...)          // message_id
	body = append(body, []byte("210301120000000+\x00")...) // final_date
	body = append(body, MsgStateDelivered)                 // message_state
	body = append(body, 0x00)                              // error_code

	msgID, finalDate, msgState, errCode := ParseQuerySMResp(body)
	if msgID != "MSG-42" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-42")
	}
	if finalDate != "210301120000000+" {
		t.Errorf("final_date = %q, want %q", finalDate, "210301120000000+")
	}
	if msgState != MsgStateDelivered {
		t.Errorf("message_state = %d, want %d", msgState, MsgStateDelivered)
	}
	if errCode != 0x00 {
		t.Errorf("error_code = 0x%02X, want 0x00", errCode)
	}
}

func TestParseQuerySMResp_EmptyBody(t *testing.T) {
	msgID, finalDate, msgState, errCode := ParseQuerySMResp(nil)
	if msgID != "" || finalDate != "" || msgState != 0 || errCode != 0 {
		t.Errorf("expected all zero values, got %q %q %d %d", msgID, finalDate, msgState, errCode)
	}
}

// =============================================================================
// cancel_sm encode/decode roundtrip
// =============================================================================

func TestCancelSM_Roundtrip(t *testing.T) {
	body := EncodeCancelSM("", "MSG-99", "+27830001234", 0x01, 0x01, "+27830005678", 0x01, 0x01)

	offset := 0
	svcType, offset := readCString(body, offset)
	if svcType != "" {
		t.Errorf("service_type = %q, want empty", svcType)
	}

	msgID, offset := readCString(body, offset)
	if msgID != "MSG-99" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-99")
	}

	if body[offset] != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++

	srcAddr, offset := readCString(body, offset)
	if srcAddr != "+27830001234" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "+27830001234")
	}

	if body[offset] != 0x01 {
		t.Errorf("dest_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("dest_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++

	destAddr, _ := readCString(body, offset)
	if destAddr != "+27830005678" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "+27830005678")
	}
}

// =============================================================================
// replace_sm encode/decode roundtrip
// =============================================================================

func TestReplaceSM_Roundtrip(t *testing.T) {
	msg := []byte("New message content")
	body := EncodeReplaceSM("MSG-100", "+27830001234", 0x01, 0x01,
		"", "", 0x01, 0x00, byte(len(msg)), msg)

	offset := 0
	msgID, offset := readCString(body, offset)
	if msgID != "MSG-100" {
		t.Errorf("message_id = %q, want %q", msgID, "MSG-100")
	}

	if body[offset] != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++

	srcAddr, offset := readCString(body, offset)
	if srcAddr != "+27830001234" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "+27830001234")
	}

	// schedule_delivery_time (empty)
	sdt, offset := readCString(body, offset)
	if sdt != "" {
		t.Errorf("schedule_delivery_time = %q, want empty", sdt)
	}

	// validity_period (empty)
	vp, offset := readCString(body, offset)
	if vp != "" {
		t.Errorf("validity_period = %q, want empty", vp)
	}

	if body[offset] != 0x01 {
		t.Errorf("registered_delivery = 0x%02X, want 0x01", body[offset])
	}
	offset++

	offset++ // sm_default_msg_id

	smLen := int(body[offset])
	offset++
	if smLen != len(msg) {
		t.Errorf("sm_length = %d, want %d", smLen, len(msg))
	}

	got := string(body[offset : offset+smLen])
	if got != string(msg) {
		t.Errorf("short_message = %q, want %q", got, string(msg))
	}
}

// =============================================================================
// data_sm encode/decode roundtrip
// =============================================================================

func TestDataSM_Roundtrip(t *testing.T) {
	body := EncodeDataSM("", 0x01, 0x01, "+27830001234",
		0x01, 0x01, "+27830005678", 0x00, 0x01, 0x00)

	offset := 0
	svcType, offset := readCString(body, offset)
	if svcType != "" {
		t.Errorf("service_type = %q, want empty", svcType)
	}

	if body[offset] != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++

	srcAddr, offset := readCString(body, offset)
	if srcAddr != "+27830001234" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "+27830001234")
	}

	if body[offset] != 0x01 {
		t.Errorf("dest_addr_ton = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("dest_addr_npi = 0x%02X, want 0x01", body[offset])
	}
	offset++

	destAddr, offset := readCString(body, offset)
	if destAddr != "+27830005678" {
		t.Errorf("dest_addr = %q, want %q", destAddr, "+27830005678")
	}

	if body[offset] != 0x00 {
		t.Errorf("esm_class = 0x%02X, want 0x00", body[offset])
	}
	offset++
	if body[offset] != 0x01 {
		t.Errorf("registered_delivery = 0x%02X, want 0x01", body[offset])
	}
	offset++
	if body[offset] != 0x00 {
		t.Errorf("data_coding = 0x%02X, want 0x00", body[offset])
	}
}

func TestParseDataSMResp(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		wantID  string
	}{
		{"normal message_id", []byte("DATA-001\x00"), "DATA-001"},
		{"empty body", nil, ""},
		{"just null", []byte{0x00}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDataSMResp(tt.body)
			if got != tt.wantID {
				t.Errorf("ParseDataSMResp() = %q, want %q", got, tt.wantID)
			}
		})
	}
}

// =============================================================================
// submit_multi encode/decode roundtrip
// =============================================================================

func TestEncodeSubmitMulti_MultipleDests(t *testing.T) {
	dests := []DestAddress{
		{Flag: 0x01, TON: 0x01, NPI: 0x01, Address: "+27830001111"},
		{Flag: 0x01, TON: 0x01, NPI: 0x01, Address: "+27830002222"},
		{Flag: 0x02, Address: "MyDL"},
	}
	msg := []byte("Hello multi")
	body := EncodeSubmitMulti("", 0x05, 0x00, "SRC", dests,
		0x00, 0x00, 0x00, "", "", 0x01, 0x00, msg)

	if len(body) == 0 {
		t.Fatal("EncodeSubmitMulti() returned empty body")
	}

	// Manually parse to verify structure.
	offset := 0
	svcType, offset := readCString(body, offset)
	if svcType != "" {
		t.Errorf("service_type = %q, want empty", svcType)
	}

	if body[offset] != 0x05 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x05", body[offset])
	}
	offset++
	offset++ // source_addr_npi

	srcAddr, offset := readCString(body, offset)
	if srcAddr != "SRC" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "SRC")
	}

	// number_of_dests
	numDests := int(body[offset])
	offset++
	if numDests != 3 {
		t.Errorf("number_of_dests = %d, want 3", numDests)
	}

	// Dest 1: SME address
	if body[offset] != 0x01 {
		t.Errorf("dest[0].flag = 0x%02X, want 0x01", body[offset])
	}
	offset++
	offset += 2 // TON + NPI
	addr1, offset := readCString(body, offset)
	if addr1 != "+27830001111" {
		t.Errorf("dest[0].address = %q, want %q", addr1, "+27830001111")
	}

	// Dest 2: SME address
	if body[offset] != 0x01 {
		t.Errorf("dest[1].flag = 0x%02X, want 0x01", body[offset])
	}
	offset++
	offset += 2 // TON + NPI
	addr2, offset := readCString(body, offset)
	if addr2 != "+27830002222" {
		t.Errorf("dest[1].address = %q, want %q", addr2, "+27830002222")
	}

	// Dest 3: Distribution list
	if body[offset] != 0x02 {
		t.Errorf("dest[2].flag = 0x%02X, want 0x02", body[offset])
	}
	offset++
	dlName, offset := readCString(body, offset)
	if dlName != "MyDL" {
		t.Errorf("dest[2].address = %q, want %q", dlName, "MyDL")
	}

	// Skip remaining mandatory fields to find short_message.
	offset++ // esm_class
	offset++ // protocol_id
	offset++ // priority_flag
	_, offset = readCString(body, offset) // schedule_delivery_time
	_, offset = readCString(body, offset) // validity_period
	offset++                              // registered_delivery
	offset++                              // replace_if_present_flag
	offset++                              // data_coding
	offset++                              // sm_default_msg_id

	smLen := int(body[offset])
	offset++
	if smLen != len(msg) {
		t.Errorf("sm_length = %d, want %d", smLen, len(msg))
	}

	got := string(body[offset : offset+smLen])
	if got != string(msg) {
		t.Errorf("short_message = %q, want %q", got, string(msg))
	}
}

func TestParseSubmitMultiResp_WithUnsuccess(t *testing.T) {
	// Build a submit_multi_resp body.
	var body []byte
	body = append(body, []byte("MULTI-001\x00")...) // message_id
	body = append(body, 0x02)                        // no_unsuccess = 2

	// Unsuccess entry 1
	body = append(body, 0x01)                         // dest_addr_ton
	body = append(body, 0x01)                         // dest_addr_npi
	body = append(body, []byte("+27830001111\x00")...) // destination_addr
	errBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(errBuf, StatusInvDstAdr)
	body = append(body, errBuf...)

	// Unsuccess entry 2
	body = append(body, 0x01)                         // dest_addr_ton
	body = append(body, 0x01)                         // dest_addr_npi
	body = append(body, []byte("+27830002222\x00")...) // destination_addr
	binary.BigEndian.PutUint32(errBuf, StatusThrottled)
	body = append(body, errBuf...)

	msgID, unsuccess := ParseSubmitMultiResp(body)
	if msgID != "MULTI-001" {
		t.Errorf("message_id = %q, want %q", msgID, "MULTI-001")
	}
	if len(unsuccess) != 2 {
		t.Fatalf("unsuccess count = %d, want 2", len(unsuccess))
	}

	if unsuccess[0].Address != "+27830001111" {
		t.Errorf("unsuccess[0].Address = %q, want %q", unsuccess[0].Address, "+27830001111")
	}
	if unsuccess[0].ErrorCode != StatusInvDstAdr {
		t.Errorf("unsuccess[0].ErrorCode = 0x%08X, want 0x%08X", unsuccess[0].ErrorCode, StatusInvDstAdr)
	}

	if unsuccess[1].Address != "+27830002222" {
		t.Errorf("unsuccess[1].Address = %q, want %q", unsuccess[1].Address, "+27830002222")
	}
	if unsuccess[1].ErrorCode != StatusThrottled {
		t.Errorf("unsuccess[1].ErrorCode = 0x%08X, want 0x%08X", unsuccess[1].ErrorCode, StatusThrottled)
	}
}

func TestParseSubmitMultiResp_NoUnsuccess(t *testing.T) {
	var body []byte
	body = append(body, []byte("MULTI-OK\x00")...) // message_id
	body = append(body, 0x00)                       // no_unsuccess = 0

	msgID, unsuccess := ParseSubmitMultiResp(body)
	if msgID != "MULTI-OK" {
		t.Errorf("message_id = %q, want %q", msgID, "MULTI-OK")
	}
	if len(unsuccess) != 0 {
		t.Errorf("unsuccess count = %d, want 0", len(unsuccess))
	}
}

func TestParseSubmitMultiResp_EmptyBody(t *testing.T) {
	msgID, unsuccess := ParseSubmitMultiResp(nil)
	if msgID != "" {
		t.Errorf("message_id = %q, want empty", msgID)
	}
	if unsuccess != nil {
		t.Errorf("unsuccess = %v, want nil", unsuccess)
	}
}

// =============================================================================
// alert_notification parse
// =============================================================================

func TestParseAlertNotification(t *testing.T) {
	var body []byte
	body = append(body, 0x01)                          // source_addr_ton
	body = append(body, 0x01)                          // source_addr_npi
	body = append(body, []byte("+27830001234\x00")...) // source_addr
	body = append(body, 0x01)                          // esme_addr_ton
	body = append(body, 0x00)                          // esme_addr_npi
	body = append(body, []byte("ESME1\x00")...)        // esme_addr

	srcTON, srcNPI, srcAddr, esmeTON, esmeNPI, esmeAddr := ParseAlertNotification(body)

	if srcTON != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", srcTON)
	}
	if srcNPI != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", srcNPI)
	}
	if srcAddr != "+27830001234" {
		t.Errorf("source_addr = %q, want %q", srcAddr, "+27830001234")
	}
	if esmeTON != 0x01 {
		t.Errorf("esme_addr_ton = 0x%02X, want 0x01", esmeTON)
	}
	if esmeNPI != 0x00 {
		t.Errorf("esme_addr_npi = 0x%02X, want 0x00", esmeNPI)
	}
	if esmeAddr != "ESME1" {
		t.Errorf("esme_addr = %q, want %q", esmeAddr, "ESME1")
	}
}

func TestParseAlertNotification_EmptyBody(t *testing.T) {
	srcTON, srcNPI, srcAddr, esmeTON, esmeNPI, esmeAddr := ParseAlertNotification(nil)
	if srcTON != 0 || srcNPI != 0 || srcAddr != "" || esmeTON != 0 || esmeNPI != 0 || esmeAddr != "" {
		t.Errorf("expected all zero values for empty body")
	}
}

func TestParseAlertNotification_TruncatedBody(t *testing.T) {
	// Only source_addr_ton — should not panic.
	body := []byte{0x01, 0x01}
	srcTON, srcNPI, srcAddr, _, _, _ := ParseAlertNotification(body)
	if srcTON != 0x01 {
		t.Errorf("source_addr_ton = 0x%02X, want 0x01", srcTON)
	}
	if srcNPI != 0x01 {
		t.Errorf("source_addr_npi = 0x%02X, want 0x01", srcNPI)
	}
	// srcAddr will be empty since there's no null-terminated string.
	if srcAddr != "" {
		t.Errorf("source_addr = %q, want empty", srcAddr)
	}
}

// =============================================================================
// MandatoryBodyLen for new operations
// =============================================================================

func TestMandatoryBodyLen_QuerySM(t *testing.T) {
	body := EncodeQuerySM("MSG-1", "+1234", 0x01, 0x01)
	got := MandatoryBodyLen(CmdQuerySM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(query_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_QuerySMResp(t *testing.T) {
	var body []byte
	body = append(body, []byte("MSG-1\x00")...)
	body = append(body, []byte("210301120000000+\x00")...)
	body = append(body, MsgStateDelivered)
	body = append(body, 0x00)

	got := MandatoryBodyLen(CmdQuerySMResp, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(query_sm_resp) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_CancelSM(t *testing.T) {
	body := EncodeCancelSM("", "MSG-1", "+1234", 0x01, 0x01, "+5678", 0x01, 0x01)
	got := MandatoryBodyLen(CmdCancelSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(cancel_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_CancelSMResp(t *testing.T) {
	got := MandatoryBodyLen(CmdCancelSMResp, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(cancel_sm_resp) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_ReplaceSM(t *testing.T) {
	msg := []byte("test")
	body := EncodeReplaceSM("MSG-1", "+1234", 0x01, 0x01, "", "", 0x01, 0x00, byte(len(msg)), msg)
	got := MandatoryBodyLen(CmdReplaceSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(replace_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_ReplaceSMResp(t *testing.T) {
	got := MandatoryBodyLen(CmdReplaceSMResp, nil)
	if got != 0 {
		t.Errorf("MandatoryBodyLen(replace_sm_resp) = %d, want 0", got)
	}
}

func TestMandatoryBodyLen_DataSM(t *testing.T) {
	body := EncodeDataSM("", 0x01, 0x01, "+1234", 0x01, 0x01, "+5678", 0x00, 0x01, 0x00)
	got := MandatoryBodyLen(CmdDataSM, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(data_sm) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_DataSMResp(t *testing.T) {
	body := []byte("DATA-001\x00")
	got := MandatoryBodyLen(CmdDataSMResp, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(data_sm_resp) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_AlertNotification(t *testing.T) {
	var body []byte
	body = append(body, 0x01)
	body = append(body, 0x01)
	body = append(body, []byte("+1234\x00")...)
	body = append(body, 0x01)
	body = append(body, 0x00)
	body = append(body, []byte("ESME1\x00")...)

	got := MandatoryBodyLen(CmdAlertNotification, body)
	if got != len(body) {
		t.Errorf("MandatoryBodyLen(alert_notification) = %d, want %d", got, len(body))
	}
}

func TestMandatoryBodyLen_SubmitMulti(t *testing.T) {
	// submit_multi should return -1 (not supported for TLV extraction).
	got := MandatoryBodyLen(CmdSubmitMulti, []byte{0x00})
	if got != -1 {
		t.Errorf("MandatoryBodyLen(submit_multi) = %d, want -1", got)
	}
}

func TestMandatoryBodyLen_SubmitMultiResp(t *testing.T) {
	got := MandatoryBodyLen(CmdSubmitMultiResp, []byte{0x00})
	if got != -1 {
		t.Errorf("MandatoryBodyLen(submit_multi_resp) = %d, want -1", got)
	}
}
