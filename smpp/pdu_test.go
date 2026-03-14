package smpp

import (
	"encoding/binary"
	"testing"
)

// =============================================================================
// DecodePDU tests
// =============================================================================
//
// Test vectors informed by cloudhopper-smpp PduDecoderTest.java but adapted
// for our implementation. We verify spec compliance against SMPP 3.4 §3.2
// (PDU format). When a test fails, RCA first — some open-source fixtures
// encode vendor-specific behavior, not spec mandates.

func TestDecodePDU(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantErr   bool
		wantCmdID uint32
		wantSeq   uint32
		wantBody  int // expected body length (-1 = don't check)
	}{
		{
			name:      "valid enquire_link (header only, no body)",
			data:      buildPDU(16, CmdEnquireLink, StatusOK, 1, nil),
			wantCmdID: CmdEnquireLink,
			wantSeq:   1,
			wantBody:  0,
		},
		{
			name:      "valid submit_sm_resp with message_id body",
			data:      buildPDU(0, CmdSubmitSMResp, StatusOK, 42, []byte("MSG-001\x00")),
			wantCmdID: CmdSubmitSMResp,
			wantSeq:   42,
			wantBody:  8, // "MSG-001" + null
		},
		{
			name:    "data shorter than PDU header (8 bytes)",
			data:    make([]byte, 8),
			wantErr: true,
		},
		{
			name:    "empty data (0 bytes)",
			data:    nil,
			wantErr: true,
		},
		{
			name:    "command_length exceeds available data",
			data:    buildPDU(100, CmdEnquireLink, StatusOK, 1, nil), // says 100 bytes, only 16 provided
			wantErr: true,
		},
		{
			name:      "command_length equals header exactly (16)",
			data:      buildPDU(16, CmdSubmitSMResp, StatusOK, 99, nil),
			wantCmdID: CmdSubmitSMResp,
			wantSeq:   99,
			wantBody:  0,
		},
		{
			name: "extra trailing data beyond command_length is ignored",
			// PDU says 16 bytes, but we pass 32 — extra should be ignored.
			data: func() []byte {
				d := make([]byte, 32)
				binary.BigEndian.PutUint32(d[0:4], 16)
				binary.BigEndian.PutUint32(d[4:8], CmdEnquireLinkResp)
				binary.BigEndian.PutUint32(d[8:12], StatusOK)
				binary.BigEndian.PutUint32(d[12:16], 7)
				// bytes 16-31 are garbage
				for i := 16; i < 32; i++ {
					d[i] = 0xFF
				}
				return d
			}(),
			wantCmdID: CmdEnquireLinkResp,
			wantSeq:   7,
			wantBody:  0,
		},
		{
			name:      "non-zero command_status (error response)",
			data:      buildPDU(0, CmdSubmitSMResp, StatusThrottled, 10, []byte{0x00}),
			wantCmdID: CmdSubmitSMResp,
			wantSeq:   10,
			wantBody:  1,
		},
		{
			name: "unknown command_id (vendor-specific) — should decode without error",
			// Per SMPP 3.4 §3.2: unknown command IDs should be handled
			// at a higher layer (generic_nack), not at the PDU decode level.
			data:      buildPDU(0, 0xDEADBEEF, StatusOK, 1, nil),
			wantCmdID: 0xDEADBEEF,
			wantSeq:   1,
			wantBody:  0,
		},
		{
			name:      "max sequence number (0xFFFFFFFF)",
			data:      buildPDU(0, CmdEnquireLink, StatusOK, 0xFFFFFFFF, nil),
			wantCmdID: CmdEnquireLink,
			wantSeq:   0xFFFFFFFF,
			wantBody:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pdu, err := DecodePDU(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pdu.CommandID != tt.wantCmdID {
				t.Errorf("CommandID = 0x%08X, want 0x%08X", pdu.CommandID, tt.wantCmdID)
			}
			if pdu.SequenceNumber != tt.wantSeq {
				t.Errorf("SequenceNumber = %d, want %d", pdu.SequenceNumber, tt.wantSeq)
			}
			if tt.wantBody >= 0 && len(pdu.Body) != tt.wantBody {
				t.Errorf("Body length = %d, want %d", len(pdu.Body), tt.wantBody)
			}
		})
	}
}

// =============================================================================
// EncodePDU / DecodePDU roundtrip
// =============================================================================

func TestEncodePDU_Roundtrip(t *testing.T) {
	tests := []struct {
		name string
		pdu  *PDU
	}{
		{
			name: "enquire_link (no body)",
			pdu:  &PDU{CommandID: CmdEnquireLink, CommandStatus: StatusOK, SequenceNumber: 1},
		},
		{
			name: "submit_sm_resp with message_id",
			pdu:  &PDU{CommandID: CmdSubmitSMResp, CommandStatus: StatusOK, SequenceNumber: 42, Body: []byte("GW-123\x00")},
		},
		{
			name: "deliver_sm_resp with error status",
			pdu:  &PDU{CommandID: CmdDeliverSMResp, CommandStatus: StatusSysErr, SequenceNumber: 99, Body: []byte{0x00}},
		},
		{
			name: "generic_nack",
			pdu:  &PDU{CommandID: CmdGenericNack, CommandStatus: StatusInvCmdID, SequenceNumber: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodePDU(tt.pdu)
			decoded, err := DecodePDU(encoded)
			if err != nil {
				t.Fatalf("DecodePDU failed: %v", err)
			}
			if decoded.CommandID != tt.pdu.CommandID {
				t.Errorf("CommandID = 0x%08X, want 0x%08X", decoded.CommandID, tt.pdu.CommandID)
			}
			if decoded.CommandStatus != tt.pdu.CommandStatus {
				t.Errorf("CommandStatus = 0x%08X, want 0x%08X", decoded.CommandStatus, tt.pdu.CommandStatus)
			}
			if decoded.SequenceNumber != tt.pdu.SequenceNumber {
				t.Errorf("SequenceNumber = %d, want %d", decoded.SequenceNumber, tt.pdu.SequenceNumber)
			}
			if len(decoded.Body) != len(tt.pdu.Body) {
				t.Errorf("Body length = %d, want %d", len(decoded.Body), len(tt.pdu.Body))
			}
			// Verify CommandLength was set correctly.
			expectedLen := uint32(16 + len(tt.pdu.Body))
			if decoded.CommandLength != expectedLen {
				t.Errorf("CommandLength = %d, want %d", decoded.CommandLength, expectedLen)
			}
		})
	}
}

// =============================================================================
// readCString tests
// =============================================================================
//
// SMPP 3.4 §3.1: C-Octet String is a null-terminated sequence of ASCII characters.

func TestReadCString(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		offset     int
		wantStr    string
		wantOffset int
	}{
		{
			name:       "normal string",
			data:       []byte("hello\x00world\x00"),
			offset:     0,
			wantStr:    "hello",
			wantOffset: 6, // past the null
		},
		{
			name:       "second string in sequence",
			data:       []byte("hello\x00world\x00"),
			offset:     6,
			wantStr:    "world",
			wantOffset: 12,
		},
		{
			name:       "empty string (just null terminator)",
			data:       []byte{0x00, 0x41},
			offset:     0,
			wantStr:    "",
			wantOffset: 1,
		},
		{
			name: "no null terminator (end of data) — graceful handling",
			// Per our implementation: returns the remaining bytes as string,
			// offset at end of data. This is lenient but prevents panics
			// when processing malformed PDUs from non-compliant SMSCs.
			data:       []byte("abc"),
			offset:     0,
			wantStr:    "abc",
			wantOffset: 3,
		},
		{
			name:       "offset at end of data",
			data:       []byte{0x00},
			offset:     1,
			wantStr:    "",
			wantOffset: 1,
		},
		{
			name:       "offset beyond data",
			data:       []byte{0x00},
			offset:     5,
			wantStr:    "",
			wantOffset: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, offset := readCString(tt.data, tt.offset)
			if s != tt.wantStr {
				t.Errorf("string = %q, want %q", s, tt.wantStr)
			}
			if offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", offset, tt.wantOffset)
			}
		})
	}
}

// =============================================================================
// ParseSubmitSMResp tests
// =============================================================================

func TestParseSubmitSMResp(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		wantID   string
	}{
		{"normal message_id", []byte("MOCK-12345\x00"), "MOCK-12345"},
		{"gateway-style ID", []byte("GW-42\x00"), "GW-42"},
		{"empty body", nil, ""},
		{"just null terminator", []byte{0x00}, ""},
		{"long ID (50 chars)", []byte("550e8400-e29b-41d4-a716-446655440000-extra-data\x00"), "550e8400-e29b-41d4-a716-446655440000-extra-data"},
		{"hex-style ID", []byte("0x1A2B3C4D\x00"), "0x1A2B3C4D"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSubmitSMResp(tt.body)
			if got != tt.wantID {
				t.Errorf("ParseSubmitSMResp() = %q, want %q", got, tt.wantID)
			}
		})
	}
}

// =============================================================================
// ParseDeliverSM tests
// =============================================================================
//
// Fixtures modeled after cloudhopper-smpp DeliverSMTest.java but built
// manually to match our parsing expectations.

func TestParseDeliverSM(t *testing.T) {
	tests := []struct {
		name           string
		body           []byte
		wantSource     string
		wantDest       string
		wantESMClass   byte
		wantMsgLen     int  // -1 = nil message expected
	}{
		{
			name:         "standard DLR deliver_sm",
			body:         buildDeliverSMBody("+27830001234", "GATEWAY", 0x04, []byte("id:GW-1 stat:DELIVRD err:000 text:")),
			wantSource:   "+27830001234",
			wantDest:     "GATEWAY",
			wantESMClass: 0x04,
			wantMsgLen:   34,
		},
		{
			name:         "MO deliver_sm (not DLR)",
			body:         buildDeliverSMBody("+27830005678", "12345", 0x00, []byte("Hello from handset")),
			wantSource:   "+27830005678",
			wantDest:     "12345",
			wantESMClass: 0x00,
			wantMsgLen:   18,
		},
		{
			name:         "empty body",
			body:         nil,
			wantSource:   "",
			wantDest:     "",
			wantESMClass: 0x00,
			wantMsgLen:   -1,
		},
		{
			name:         "empty short_message (sm_length=0)",
			body:         buildDeliverSMBody("+27830009999", "GW", 0x04, nil),
			wantSource:   "+27830009999",
			wantDest:     "GW",
			wantESMClass: 0x04,
			wantMsgLen:   -1,
		},
		{
			name:         "binary payload (all byte values 0x00-0x8F)",
			body:         buildDeliverSMBody("SRC", "DST", 0x40, makeBinaryPayload(0x90)),
			wantSource:   "SRC",
			wantDest:     "DST",
			wantESMClass: 0x40,
			wantMsgLen:   0x90,
		},
		{
			name:         "UCS-2 DLR with null bytes in message",
			body:         buildDeliverSMBody("+1234", "+5678", 0x04, []byte{0x00, 0x48, 0x00, 0x69}),
			wantSource:   "+1234",
			wantDest:     "+5678",
			wantESMClass: 0x04,
			wantMsgLen:   4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dst, esm, msg := ParseDeliverSM(tt.body)
			if src != tt.wantSource {
				t.Errorf("sourceAddr = %q, want %q", src, tt.wantSource)
			}
			if dst != tt.wantDest {
				t.Errorf("destAddr = %q, want %q", dst, tt.wantDest)
			}
			if esm != tt.wantESMClass {
				t.Errorf("esmClass = 0x%02X, want 0x%02X", esm, tt.wantESMClass)
			}
			if tt.wantMsgLen < 0 {
				if msg != nil {
					t.Errorf("shortMessage = %v, want nil", msg)
				}
			} else {
				if len(msg) != tt.wantMsgLen {
					t.Errorf("shortMessage length = %d, want %d", len(msg), tt.wantMsgLen)
				}
			}
		})
	}
}

func TestParseDeliverSM_TruncatedBody(t *testing.T) {
	// A truncated PDU body should not panic — it should return whatever
	// it could parse. This tests resilience against malformed PDUs from
	// non-compliant SMSCs. Modeled after cloudhopper PduDecoderTest
	// truncation edge cases.
	full := buildDeliverSMBody("+27830001234", "GATEWAY", 0x04, []byte("test"))

	// Truncate at various points and verify no panic.
	truncPoints := []int{0, 1, 3, 5, 10, 15, 20, len(full) - 2, len(full) - 1}
	for _, n := range truncPoints {
		if n > len(full) {
			continue
		}
		t.Run("", func(t *testing.T) {
			// Should not panic with truncated data.
			ParseDeliverSM(full[:n])
		})
	}
}

// =============================================================================
// EncodeSubmitSM tests
// =============================================================================

func TestEncodeSubmitSM(t *testing.T) {
	t.Run("short message under 254 bytes", func(t *testing.T) {
		msg := []byte("Hello world")
		body := EncodeSubmitSM("SRC", 0x05, 0x00, "+27830001234", 0x01, 0x01,
			0x00, 0x00, 0x00, 0x01, msg)

		// Verify by parsing the addresses back.
		src, dst, esm, parsed := parseSubmitSMBody(body)
		if src != "SRC" {
			t.Errorf("source = %q, want %q", src, "SRC")
		}
		if dst != "+27830001234" {
			t.Errorf("dest = %q, want %q", dst, "+27830001234")
		}
		if esm != 0x00 {
			t.Errorf("esm_class = 0x%02X, want 0x00", esm)
		}
		if string(parsed) != string(msg) {
			t.Errorf("message = %q, want %q", string(parsed), string(msg))
		}
	})

	t.Run("message exactly 254 bytes uses short_message field", func(t *testing.T) {
		msg := make([]byte, 254)
		for i := range msg {
			msg[i] = byte(i % 256)
		}
		body := EncodeSubmitSM("S", 0x05, 0x00, "D", 0x01, 0x01,
			0x00, 0x00, 0x00, 0x01, msg)

		// sm_length should be 254 (0xFE), not using TLV.
		_, _, _, parsed := parseSubmitSMBody(body)
		if len(parsed) != 254 {
			t.Errorf("message length = %d, want 254", len(parsed))
		}
	})

	t.Run("message 255+ bytes uses message_payload TLV", func(t *testing.T) {
		msg := make([]byte, 300)
		for i := range msg {
			msg[i] = byte(i % 256)
		}
		body := EncodeSubmitSM("S", 0x05, 0x00, "D", 0x01, 0x01,
			0x00, 0x00, 0x00, 0x01, msg)

		// Find sm_length — should be 0 since message is in TLV.
		_, _, _, parsed := parseSubmitSMBody(body)
		if parsed != nil {
			t.Errorf("short_message should be nil when using TLV, got %d bytes", len(parsed))
		}

		// Verify TLV is present: find tag 0x0424 after sm_length=0.
		tlvPayload := extractMessagePayloadTLV(body)
		if tlvPayload == nil {
			t.Fatal("message_payload TLV (0x0424) not found")
		}
		if len(tlvPayload) != 300 {
			t.Errorf("TLV payload length = %d, want 300", len(tlvPayload))
		}
		// Verify payload content is preserved byte-for-byte.
		for i, b := range tlvPayload {
			if b != msg[i] {
				t.Errorf("TLV payload[%d] = 0x%02X, want 0x%02X", i, b, msg[i])
				break
			}
		}
	})

	t.Run("empty message", func(t *testing.T) {
		body := EncodeSubmitSM("S", 0x05, 0x00, "D", 0x01, 0x01,
			0x00, 0x00, 0x00, 0x01, nil)

		_, _, _, parsed := parseSubmitSMBody(body)
		if parsed != nil {
			t.Errorf("expected nil message, got %d bytes", len(parsed))
		}
	})
}

// =============================================================================
// Helpers
// =============================================================================

// buildPDU constructs a raw PDU byte slice. If cmdLen is 0, it is computed
// from the header + body.
func buildPDU(cmdLen uint32, cmdID, status, seq uint32, body []byte) []byte {
	if cmdLen == 0 {
		cmdLen = uint32(16 + len(body))
	}
	data := make([]byte, 16+len(body))
	binary.BigEndian.PutUint32(data[0:4], cmdLen)
	binary.BigEndian.PutUint32(data[4:8], cmdID)
	binary.BigEndian.PutUint32(data[8:12], status)
	binary.BigEndian.PutUint32(data[12:16], seq)
	copy(data[16:], body)
	return data
}

// buildDeliverSMBody constructs a deliver_sm body with the given fields.
// Follows SMPP 3.4 §4.6.1 deliver_sm body layout.
func buildDeliverSMBody(sourceAddr, destAddr string, esmClass byte, shortMessage []byte) []byte {
	var buf []byte
	buf = append(buf, 0x00) // service_type (empty C-string)
	buf = append(buf, 0x00) // source_addr_ton
	buf = append(buf, 0x00) // source_addr_npi
	buf = append(buf, []byte(sourceAddr)...)
	buf = append(buf, 0x00) // null terminator
	buf = append(buf, 0x01) // dest_addr_ton
	buf = append(buf, 0x01) // dest_addr_npi
	buf = append(buf, []byte(destAddr)...)
	buf = append(buf, 0x00) // null terminator
	buf = append(buf, esmClass)
	buf = append(buf, 0x00) // protocol_id
	buf = append(buf, 0x00) // priority_flag
	buf = append(buf, 0x00) // schedule_delivery_time (empty)
	buf = append(buf, 0x00) // validity_period (empty)
	buf = append(buf, 0x00) // registered_delivery
	buf = append(buf, 0x00) // replace_if_present_flag
	buf = append(buf, 0x00) // data_coding
	buf = append(buf, 0x00) // sm_default_msg_id
	if shortMessage == nil {
		buf = append(buf, 0x00) // sm_length = 0
	} else {
		buf = append(buf, byte(len(shortMessage)))
		buf = append(buf, shortMessage...)
	}
	return buf
}

// parseSubmitSMBody extracts key fields from a submit_sm body for verification.
// Mirrors ParseDeliverSM's logic since submit_sm has the same field layout.
func parseSubmitSMBody(body []byte) (sourceAddr, destAddr string, esmClass byte, shortMessage []byte) {
	return ParseDeliverSM(body)
}

// extractMessagePayloadTLV scans a submit_sm body for the message_payload TLV
// (tag=0x0424) and returns the TLV value bytes.
func extractMessagePayloadTLV(body []byte) []byte {
	// Skip to the end of sm_length + short_message to find TLVs.
	offset := 0
	// service_type
	for offset < len(body) && body[offset] != 0x00 {
		offset++
	}
	offset++ // skip null
	offset += 2 // source TON+NPI
	for offset < len(body) && body[offset] != 0x00 {
		offset++
	}
	offset++ // skip null
	offset += 2 // dest TON+NPI
	for offset < len(body) && body[offset] != 0x00 {
		offset++
	}
	offset++ // skip null
	offset++ // esm_class
	offset++ // protocol_id
	offset++ // priority_flag
	// schedule_delivery_time (empty C-string)
	for offset < len(body) && body[offset] != 0x00 {
		offset++
	}
	offset++
	// validity_period (empty C-string)
	for offset < len(body) && body[offset] != 0x00 {
		offset++
	}
	offset++
	offset++ // registered_delivery
	offset++ // replace_if_present_flag
	offset++ // data_coding
	offset++ // sm_default_msg_id

	if offset >= len(body) {
		return nil
	}
	smLen := int(body[offset])
	offset++
	offset += smLen // skip short_message

	// Now scan for TLVs.
	for offset+4 <= len(body) {
		tag := binary.BigEndian.Uint16(body[offset : offset+2])
		tlvLen := int(binary.BigEndian.Uint16(body[offset+2 : offset+4]))
		offset += 4
		if tag == 0x0424 && offset+tlvLen <= len(body) {
			return body[offset : offset+tlvLen]
		}
		offset += tlvLen
	}
	return nil
}

// makeBinaryPayload creates a byte slice of length n with values 0x00..0xFF cycling.
func makeBinaryPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 256)
	}
	return b
}
