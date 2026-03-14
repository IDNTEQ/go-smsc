package smpp

import (
	"testing"
)

// =============================================================================
// ParseDLRReceipt tests
// =============================================================================
//
// Test vectors drawn from:
//   - SMPP 3.4 specification §4.7.1 (Delivery Receipt Format)
//   - cloudhopper-smpp DeliveryReceiptTest.java (18 test methods)
//   - Jasmin DLRHandlerTest.py (hex/decimal ID conversion)
//   - ajankovic/smpp receipt_test.go (UUID-style IDs)
//   - Real-world DLR receipt samples from MNO SMSCs
//
// RCA methodology: when a test fails, we check:
//   1. Is the expected value correct per SMPP 3.4 §4.7.1?
//   2. Do real-world SMSCs actually produce this format?
//   3. Is this a spec violation we should tolerate or reject?
//
// Our regex patterns:
//   dlrIDRegex   = `id:([^\s]+)`
//   dlrStatRegex = `stat:([A-Z]{5,7})`  ← uppercase-only, 5-7 chars
//   dlrErrRegex  = `err:([^\s]+)`

func TestParseDLRReceipt(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantNil   bool   // expect nil return (not a DLR)
		wantID    string
		wantStat  string
		wantErr   string
		note      string // RCA / rationale
	}{
		// -----------------------------------------------------------------
		// Standard SMPP 3.4 §4.7.1 format
		// -----------------------------------------------------------------
		{
			name:     "standard complete receipt — DELIVRD",
			text:     "id:MOCK-12345 sub:001 dlvrd:001 submit date:0809011130 done date:0809011131 stat:DELIVRD err:000 text:Test message",
			wantID:   "MOCK-12345",
			wantStat: "DELIVRD",
			wantErr:  "000",
		},
		{
			name:     "standard complete receipt — UNDELIV with error",
			text:     "id:MSG-999 sub:001 dlvrd:000 submit date:0809011130 done date:0809011131 stat:UNDELIV err:069 text:",
			wantID:   "MSG-999",
			wantStat: "UNDELIV",
			wantErr:  "069",
		},
		{
			name:     "standard complete receipt — EXPIRED",
			text:     "id:GW-42 sub:001 dlvrd:000 submit date:0809011130 done date:0809021130 stat:EXPIRED err:000 text:",
			wantID:   "GW-42",
			wantStat: "EXPIRED",
			wantErr:  "000",
		},
		{
			name:     "standard complete receipt — DELETED",
			text:     "id:MSG-100 sub:001 dlvrd:000 submit date:0809011130 done date:0809011200 stat:DELETED err:000 text:",
			wantID:   "MSG-100",
			wantStat: "DELETED",
			wantErr:  "000",
		},
		{
			name:     "standard complete receipt — REJECTD",
			text:     "id:MSG-200 sub:001 dlvrd:000 submit date:0809011130 done date:0809011130 stat:REJECTD err:008 text:",
			wantID:   "MSG-200",
			wantStat: "REJECTD",
			wantErr:  "008",
		},
		{
			name:     "standard complete receipt — ACCEPTD",
			text:     "id:MSG-300 sub:001 dlvrd:000 submit date:0809011130 done date:0809011130 stat:ACCEPTD err:000 text:",
			wantID:   "MSG-300",
			wantStat: "ACCEPTD",
			wantErr:  "000",
		},
		{
			name:     "standard complete receipt — UNKNOWN",
			text:     "id:MSG-400 sub:001 dlvrd:000 submit date:0809011130 done date:0809011130 stat:UNKNOWN err:000 text:",
			wantID:   "MSG-400",
			wantStat: "UNKNOWN",
			wantErr:  "000",
		},
		{
			name:     "standard complete receipt — ENROUTE (7 chars, max length)",
			text:     "id:MSG-500 sub:001 dlvrd:000 submit date:0809011130 done date:0809011130 stat:ENROUTE err:000 text:",
			wantID:   "MSG-500",
			wantStat: "ENROUTE",
			wantErr:  "000",
		},

		// -----------------------------------------------------------------
		// Minimal / partial receipts
		// -----------------------------------------------------------------
		{
			name:     "minimal receipt — id and stat only",
			text:     "id:GW-1 stat:DELIVRD",
			wantID:   "GW-1",
			wantStat: "DELIVRD",
			wantErr:  "",
			note:     "Some lightweight SMSCs omit sub/dlvrd/dates/err. Valid per our parser since id: is present.",
		},
		{
			name:     "receipt with id but no stat — stat missing",
			text:     "id:GW-2 sub:001 dlvrd:001 err:000 text:",
			wantID:   "GW-2",
			wantStat: "", // stat field absent
			wantErr:  "000",
			note:     "Malformed but id: present. Parser extracts what it can.",
		},
		{
			name:     "receipt with id but no err — err missing",
			text:     "id:GW-3 stat:DELIVRD",
			wantID:   "GW-3",
			wantStat: "DELIVRD",
			wantErr:  "",
		},

		// -----------------------------------------------------------------
		// Message ID format variations (seen in production)
		// -----------------------------------------------------------------
		{
			name:     "hex-prefixed message ID (0x format)",
			text:     "id:0x1A2B3C4D sub:001 dlvrd:001 stat:DELIVRD err:000 text:",
			wantID:   "0x1A2B3C4D",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "Some SMSCs return hex-encoded integer IDs. cloudhopper DeliveryReceiptTest covers this.",
		},
		{
			name:     "UUID-style message ID",
			text:     "id:550e8400-e29b-41d4-a716-446655440000 stat:DELIVRD err:000 text:",
			wantID:   "550e8400-e29b-41d4-a716-446655440000",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "ajankovic/smpp receipt_test.go tests UUID IDs. Dashes are non-whitespace, so our regex handles this.",
		},
		{
			name:     "gateway-style message ID (GW-prefix)",
			text:     "id:GW-123456 sub:001 dlvrd:001 stat:DELIVRD err:000 text:",
			wantID:   "GW-123456",
			wantStat: "DELIVRD",
			wantErr:  "000",
		},
		{
			name:     "numeric-only message ID",
			text:     "id:123456789 sub:001 dlvrd:001 stat:DELIVRD err:000 text:",
			wantID:   "123456789",
			wantStat: "DELIVRD",
			wantErr:  "000",
		},
		{
			name:     "very long message ID (hash-like)",
			text:     "id:c449ab9744f47b6af1879e49e75e4f40 sub:001 stat:DELIVRD err:0 text:Test",
			wantID:   "c449ab9744f47b6af1879e49e75e4f40",
			wantStat: "DELIVRD",
			wantErr:  "0",
			note:     "Jasmin uses 32-char hex hashes as message IDs.",
		},

		// -----------------------------------------------------------------
		// Case sensitivity (real-world SMSC behavior)
		// -----------------------------------------------------------------
		//
		// RCA: SMPP 3.4 §4.7.1 shows status values in uppercase (DELIVRD,
		// UNDELIV, etc.) but does NOT explicitly mandate uppercase-only.
		// In practice, some Huawei and Ericsson SMSCs send lowercase or
		// mixed case. Our regex `stat:([A-Z]{5,7})` only matches uppercase.
		//
		// Verdict: Our regex is too strict. Real SMSCs send lowercase.
		// This is a BUG in our parser that must be fixed.
		{
			name:     "lowercase stat (real-world: Huawei SMSCs)",
			text:     "id:HW-001 sub:001 dlvrd:001 stat:delivrd err:000 text:",
			wantID:   "HW-001",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "BUG: dlrStatRegex requires uppercase [A-Z]. Must be case-insensitive.",
		},
		{
			name:     "mixed case stat",
			text:     "id:MX-001 sub:001 stat:Delivrd err:000 text:",
			wantID:   "MX-001",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "BUG: same case sensitivity issue.",
		},

		// -----------------------------------------------------------------
		// Whitespace and formatting variations
		// -----------------------------------------------------------------
		{
			name:     "extra whitespace between fields",
			text:     "id:WS-001  sub:001  dlvrd:001  stat:DELIVRD  err:000  text:",
			wantID:   "WS-001",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "Extra spaces between fields. Regex handles this since it scans globally.",
		},
		{
			name:     "leading/trailing whitespace",
			text:     "  id:TRIM-001 stat:DELIVRD err:000  ",
			wantID:   "TRIM-001",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "ParseDLRReceipt calls TrimSpace first.",
		},
		{
			name:     "fields in non-standard order",
			text:     "stat:DELIVRD id:ORDER-001 err:000 sub:001 text:",
			wantID:   "ORDER-001",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "Regex matches anywhere in text, so field order doesn't matter.",
		},

		// -----------------------------------------------------------------
		// Non-DLR inputs (should return nil)
		// -----------------------------------------------------------------
		{
			name:    "empty string",
			text:    "",
			wantNil: true,
		},
		{
			name:    "whitespace-only string",
			text:    "   \t\n  ",
			wantNil: true,
		},
		{
			name:    "regular SMS text (not a DLR)",
			text:    "Hello, this is a normal SMS message!",
			wantNil: true,
			note:    "No id: field, so not parsed as DLR.",
		},
		{
			name:    "text containing 'id' but not 'id:' prefix",
			text:    "Your identity has been verified.",
			wantNil: true,
		},

		// -----------------------------------------------------------------
		// Edge cases
		// -----------------------------------------------------------------
		{
			name:     "id with colon in value (unlikely but defensive)",
			text:     "id:ns:12345 stat:DELIVRD err:000",
			wantID:   "ns:12345",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "Regex [^\\s]+ captures everything until whitespace, including colons.",
		},
		{
			name:     "error code with non-numeric value",
			text:     "id:ERR-001 stat:UNDELIV err:E_NETWORK text:",
			wantID:   "ERR-001",
			wantStat: "UNDELIV",
			wantErr:  "E_NETWORK",
			note:     "Some SMSCs return text error codes. Our regex handles this ([^\\s]+).",
		},
		{
			name:     "vendor-specific extra fields (Clickatell format)",
			text:     "id:CT-001 sub:001 dlvrd:001 submit date:0809011130 done date:0809011131 stat:DELIVRD err:000 text: apiMsgId:abc123",
			wantID:   "CT-001",
			wantStat: "DELIVRD",
			wantErr:  "000",
			note:     "Extra vendor fields after text: are ignored. cloudhopper tests this.",
		},
		{
			name:     "stat field at exact minimum length (5 chars: DELIV is hypothetical)",
			text:     "id:LEN-001 stat:DELIV err:000",
			wantID:   "LEN-001",
			wantStat: "DELIV",
			wantErr:  "000",
			note:     "SMPP 3.4 standard statuses are 6-7 chars. 5 is the regex minimum.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := ParseDLRReceipt(tt.text)
			if tt.wantNil {
				if receipt != nil {
					t.Errorf("expected nil, got %+v", receipt)
				}
				return
			}
			if receipt == nil {
				t.Fatal("expected non-nil receipt, got nil")
			}
			if receipt.MessageID != tt.wantID {
				t.Errorf("MessageID = %q, want %q", receipt.MessageID, tt.wantID)
			}
			if receipt.Status != tt.wantStat {
				t.Errorf("Status = %q, want %q", receipt.Status, tt.wantStat)
			}
			if receipt.ErrorCode != tt.wantErr {
				t.Errorf("ErrorCode = %q, want %q", receipt.ErrorCode, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// IsDLR tests
// =============================================================================
//
// SMPP 3.4 §5.2.12: ESM class bits 2-5 indicate message type.
// Bit 2 (0x04) = delivery receipt.
//
// Test vectors cover all relevant bit combinations to ensure we only
// check bit 2, not adjacent bits that mean different things.

func TestIsDLR(t *testing.T) {
	tests := []struct {
		name     string
		esmClass byte
		want     bool
		note     string
	}{
		// Bit 2 NOT set — not a DLR.
		{"0x00 default/store-forward", 0x00, false, ""},
		{"0x01 datagram mode", 0x01, false, ""},
		{"0x02 forward mode", 0x02, false, ""},
		{"0x08 SME delivery ack (bit 3)", 0x08, false, "Bit 3 = SME ack, NOT a DLR"},
		{"0x10 SME manual/user ack (bit 4)", 0x10, false, ""},
		{"0x20 intermediate notification (bit 5)", 0x20, false, ""},
		{"0x40 UDH indicator only", 0x40, false, "UDH without DLR flag"},
		{"0x80 reply path (bit 7)", 0x80, false, ""},
		{"0x03 datagram+forward", 0x03, false, ""},

		// Bit 2 SET — IS a DLR.
		{"0x04 delivery receipt (canonical)", 0x04, true, ""},
		{"0x05 DLR + datagram", 0x05, true, "Bit 2 set among other bits"},
		{"0x06 DLR + forward", 0x06, true, ""},
		{"0x0C DLR + SME ack (bits 2+3)", 0x0C, true, "Both DLR and SME ack flags"},
		{"0x44 UDH + DLR", 0x44, true, "UDH indicator with DLR — common in multipart DLRs"},
		{"0x24 intermediate + DLR", 0x24, true, ""},
		{"0xC4 UDH + reply path + DLR", 0xC4, true, ""},
		{"0xFF all bits set", 0xFF, true, "Bit 2 is set among all others"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDLR(tt.esmClass)
			if got != tt.want {
				t.Errorf("IsDLR(0x%02X) = %v, want %v", tt.esmClass, got, tt.want)
			}
		})
	}
}
