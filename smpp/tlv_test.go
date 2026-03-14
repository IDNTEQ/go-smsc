package smpp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// =============================================================================
// Roundtrip encode/decode tests for each value type
// =============================================================================

func TestTLVSet_RoundtripUint8(t *testing.T) {
	ts := make(TLVSet)
	ts.SetUint8(TagMessageState, 0x02)

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	got, ok := decoded.GetUint8(TagMessageState)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got != 0x02 {
		t.Errorf("GetUint8 = 0x%02X, want 0x02", got)
	}
}

func TestTLVSet_RoundtripUint16(t *testing.T) {
	ts := make(TLVSet)
	ts.SetUint16(TagSourcePort, 0xBEEF)

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	got, ok := decoded.GetUint16(TagSourcePort)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got != 0xBEEF {
		t.Errorf("GetUint16 = 0x%04X, want 0xBEEF", got)
	}
}

func TestTLVSet_RoundtripUint32(t *testing.T) {
	ts := make(TLVSet)
	ts.SetUint32(TagQOSTimeToLive, 0xDEADBEEF)

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	got, ok := decoded.GetUint32(TagQOSTimeToLive)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got != 0xDEADBEEF {
		t.Errorf("GetUint32 = 0x%08X, want 0xDEADBEEF", got)
	}
}

func TestTLVSet_RoundtripString(t *testing.T) {
	ts := make(TLVSet)
	ts.SetString(TagReceiptedMessageID, "MSG-12345")

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	got, ok := decoded.GetString(TagReceiptedMessageID)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got != "MSG-12345" {
		t.Errorf("GetString = %q, want %q", got, "MSG-12345")
	}
}

func TestTLVSet_RoundtripBytes(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03, 0xFF, 0x00, 0xAB}
	ts := make(TLVSet)
	ts.SetBytes(TagMessagePayload, payload)

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	got, ok := decoded.GetBytes(TagMessagePayload)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("GetBytes = %v, want %v", got, payload)
	}
}

// =============================================================================
// Empty / nil edge cases
// =============================================================================

func TestTLVSet_EmptyEncodeReturnsNil(t *testing.T) {
	ts := make(TLVSet)
	encoded := ts.Encode()
	if encoded != nil {
		t.Errorf("Encode of empty TLVSet = %v, want nil", encoded)
	}
}

func TestDecodeTLVs_EmptyInput(t *testing.T) {
	ts, err := DecodeTLVs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ts) != 0 {
		t.Errorf("expected empty TLVSet, got %d entries", len(ts))
	}

	ts2, err := DecodeTLVs([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ts2) != 0 {
		t.Errorf("expected empty TLVSet, got %d entries", len(ts2))
	}
}

// =============================================================================
// Unknown tags preserved through roundtrip
// =============================================================================

func TestTLVSet_UnknownTagPreserved(t *testing.T) {
	unknownTag := Tag(0xFFFF)
	value := []byte{0xCA, 0xFE}

	ts := make(TLVSet)
	ts.SetBytes(unknownTag, value)

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	got, ok := decoded.GetBytes(unknownTag)
	if !ok {
		t.Fatal("unknown tag not preserved")
	}
	if !bytes.Equal(got, value) {
		t.Errorf("unknown tag value = %v, want %v", got, value)
	}
}

// =============================================================================
// Zero-length value TLV (valid per SMPP spec)
// =============================================================================

func TestTLVSet_ZeroLengthValue(t *testing.T) {
	// Build a zero-length TLV on the wire: tag=0x130C, length=0, no value bytes.
	wire := make([]byte, 4)
	binary.BigEndian.PutUint16(wire[0:2], uint16(TagAlertOnMessageDelivery))
	binary.BigEndian.PutUint16(wire[2:4], 0)

	decoded, err := DecodeTLVs(wire)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}
	if !decoded.Has(TagAlertOnMessageDelivery) {
		t.Fatal("zero-length TLV not preserved")
	}
	v, ok := decoded.GetBytes(TagAlertOnMessageDelivery)
	if !ok {
		t.Fatal("GetBytes returned false for zero-length value")
	}
	if len(v) != 0 {
		t.Errorf("expected empty value, got %d bytes", len(v))
	}

	// Verify roundtrip: zero-length value encodes back correctly.
	reEncoded := decoded.Encode()
	if !bytes.Equal(reEncoded, wire) {
		t.Errorf("re-encoded = %v, want %v", reEncoded, wire)
	}
}

// =============================================================================
// Truncated TLV data returns error
// =============================================================================

func TestDecodeTLVs_TruncatedHeader(t *testing.T) {
	// Only 3 bytes — not enough for a 4-byte TLV header.
	_, err := DecodeTLVs([]byte{0x04, 0x27, 0x00})
	if err == nil {
		t.Fatal("expected error for truncated TLV header, got nil")
	}
}

func TestDecodeTLVs_TruncatedValue(t *testing.T) {
	// Valid header saying length=4, but only 2 value bytes.
	data := make([]byte, 6)
	binary.BigEndian.PutUint16(data[0:2], uint16(TagMessageState))
	binary.BigEndian.PutUint16(data[2:4], 4) // declares 4 bytes
	data[4] = 0x01
	data[5] = 0x02 // only 2 bytes of value

	_, err := DecodeTLVs(data)
	if err == nil {
		t.Fatal("expected error for truncated TLV value, got nil")
	}
}

// =============================================================================
// Multiple TLVs in single encode/decode
// =============================================================================

func TestTLVSet_MultipleTLVs(t *testing.T) {
	ts := make(TLVSet)
	ts.SetUint8(TagMessageState, 0x02)
	ts.SetString(TagReceiptedMessageID, "ID-999")
	ts.SetUint16(TagSourcePort, 1234)
	ts.SetBytes(TagNetworkErrorCode, []byte{0x03, 0x00, 0x01})

	encoded := ts.Encode()
	decoded, err := DecodeTLVs(encoded)
	if err != nil {
		t.Fatalf("DecodeTLVs failed: %v", err)
	}

	if len(decoded) != 4 {
		t.Fatalf("decoded %d TLVs, want 4", len(decoded))
	}

	msgState, ok := decoded.GetUint8(TagMessageState)
	if !ok || msgState != 0x02 {
		t.Errorf("message_state = 0x%02X, ok=%v, want 0x02", msgState, ok)
	}

	msgID, ok := decoded.GetString(TagReceiptedMessageID)
	if !ok || msgID != "ID-999" {
		t.Errorf("receipted_message_id = %q, ok=%v, want %q", msgID, ok, "ID-999")
	}

	port, ok := decoded.GetUint16(TagSourcePort)
	if !ok || port != 1234 {
		t.Errorf("source_port = %d, ok=%v, want 1234", port, ok)
	}

	errCode, ok := decoded.GetBytes(TagNetworkErrorCode)
	if !ok || !bytes.Equal(errCode, []byte{0x03, 0x00, 0x01}) {
		t.Errorf("network_error_code = %v, ok=%v", errCode, ok)
	}
}

// =============================================================================
// GetString strips trailing null byte
// =============================================================================

func TestTLVSet_GetStringStripsNullTerminator(t *testing.T) {
	ts := make(TLVSet)
	// Store a value with a trailing null (as some SMSCs send).
	ts[TagReceiptedMessageID] = []byte("MSG-001\x00")

	got, ok := ts.GetString(TagReceiptedMessageID)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got != "MSG-001" {
		t.Errorf("GetString = %q, want %q", got, "MSG-001")
	}
}

func TestTLVSet_GetStringNoNull(t *testing.T) {
	ts := make(TLVSet)
	ts[TagReceiptedMessageID] = []byte("MSG-001")

	got, ok := ts.GetString(TagReceiptedMessageID)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got != "MSG-001" {
		t.Errorf("GetString = %q, want %q", got, "MSG-001")
	}
}

// =============================================================================
// Has() tests
// =============================================================================

func TestTLVSet_Has(t *testing.T) {
	ts := make(TLVSet)
	ts.SetUint8(TagMessageState, 0x01)

	if !ts.Has(TagMessageState) {
		t.Error("Has(TagMessageState) = false, want true")
	}
	if ts.Has(TagSourcePort) {
		t.Error("Has(TagSourcePort) = true, want false")
	}
}

// =============================================================================
// Sorted tag order in Encode output
// =============================================================================

func TestTLVSet_EncodeSortedOrder(t *testing.T) {
	ts := make(TLVSet)
	// Insert in reverse order to ensure sorting is applied.
	ts.SetUint8(TagCongestionState, 0xFF)       // 0x0428
	ts.SetUint8(TagMessageState, 0x02)           // 0x0427
	ts.SetUint16(TagSourcePort, 0x1234)          // 0x020A
	ts.SetUint8(TagDestAddrSubunit, 0x01)        // 0x0005

	encoded := ts.Encode()

	// Read tags from the encoded bytes and verify they are sorted.
	var tags []Tag
	offset := 0
	for offset+4 <= len(encoded) {
		tag := Tag(binary.BigEndian.Uint16(encoded[offset : offset+2]))
		length := int(binary.BigEndian.Uint16(encoded[offset+2 : offset+4]))
		tags = append(tags, tag)
		offset += 4 + length
	}

	if len(tags) != 4 {
		t.Fatalf("expected 4 tags, got %d", len(tags))
	}
	for i := 1; i < len(tags); i++ {
		if tags[i] <= tags[i-1] {
			t.Errorf("tags not sorted: tag[%d]=0x%04X <= tag[%d]=0x%04X",
				i, uint16(tags[i]), i-1, uint16(tags[i-1]))
		}
	}
}

// =============================================================================
// Getter type-mismatch returns zero, false
// =============================================================================

func TestTLVSet_GetTypeMismatch(t *testing.T) {
	ts := make(TLVSet)
	ts.SetUint32(TagQOSTimeToLive, 100)

	// Trying to read a 4-byte value as uint8 should fail.
	_, ok := ts.GetUint8(TagQOSTimeToLive)
	if ok {
		t.Error("GetUint8 on 4-byte value returned true, want false")
	}

	// Trying to read a 4-byte value as uint16 should fail.
	_, ok = ts.GetUint16(TagQOSTimeToLive)
	if ok {
		t.Error("GetUint16 on 4-byte value returned true, want false")
	}

	// Absent tag.
	_, ok = ts.GetUint8(TagSourcePort)
	if ok {
		t.Error("GetUint8 on absent tag returned true, want false")
	}
}

// =============================================================================
// SetBytes makes a defensive copy
// =============================================================================

func TestTLVSet_SetBytesCopiesInput(t *testing.T) {
	ts := make(TLVSet)
	original := []byte{0x01, 0x02, 0x03}
	ts.SetBytes(TagMessagePayload, original)

	// Mutate the original — stored value should be unaffected.
	original[0] = 0xFF
	got, ok := ts.GetBytes(TagMessagePayload)
	if !ok {
		t.Fatal("expected tag to be present")
	}
	if got[0] != 0x01 {
		t.Errorf("SetBytes did not copy: got[0] = 0x%02X, want 0x01", got[0])
	}
}

// =============================================================================
// GetBytes returns a copy (mutation does not affect TLVSet)
// =============================================================================

func TestTLVSet_GetBytesCopiesOutput(t *testing.T) {
	ts := make(TLVSet)
	ts.SetBytes(TagMessagePayload, []byte{0x01, 0x02, 0x03})

	got, _ := ts.GetBytes(TagMessagePayload)
	got[0] = 0xFF

	got2, _ := ts.GetBytes(TagMessagePayload)
	if got2[0] != 0x01 {
		t.Errorf("GetBytes did not return a copy: got2[0] = 0x%02X, want 0x01", got2[0])
	}
}

// =============================================================================
// Fuzz test for DecodeTLVs — no panics on random input
// =============================================================================

func FuzzDecodeTLVs(f *testing.F) {
	// Seed corpus with valid TLV data.
	f.Add([]byte{})
	f.Add([]byte{0x04, 0x27, 0x00, 0x01, 0x02})                         // message_state=0x02
	f.Add([]byte{0x04, 0x27, 0x00, 0x00})                                 // zero-length value
	f.Add([]byte{0x04, 0x27, 0x00, 0x01, 0x02, 0x02, 0x0A, 0x00, 0x02, 0xBE, 0xEF}) // two TLVs

	f.Fuzz(func(t *testing.T, data []byte) {
		// DecodeTLVs must never panic regardless of input.
		ts, err := DecodeTLVs(data)
		if err != nil {
			return
		}
		// If decode succeeded, re-encode should also not panic.
		_ = ts.Encode()
	})
}
