package smpp

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// GSM 7-bit basic alphabet.
// ---------------------------------------------------------------------------

func TestEncodeGSM7_BasicAlphabet(t *testing.T) {
	text := "Hello World 0123456789"
	packed, septets, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7(%q) error: %v", text, err)
	}
	if septets != len(text) {
		t.Errorf("septets = %d, want %d", septets, len(text))
	}

	decoded, err := DecodeGSM7(packed, septets)
	if err != nil {
		t.Fatalf("DecodeGSM7 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeGSM7_Punctuation(t *testing.T) {
	text := "!\"#%&'()*+,-./:;<=>?@"
	packed, septets, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7(%q) error: %v", text, err)
	}

	decoded, err := DecodeGSM7(packed, septets)
	if err != nil {
		t.Fatalf("DecodeGSM7 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeGSM7_UpperLowerCase(t *testing.T) {
	text := "ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz"
	packed, septets, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7(%q) error: %v", text, err)
	}

	decoded, err := DecodeGSM7(packed, septets)
	if err != nil {
		t.Fatalf("DecodeGSM7 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

// ---------------------------------------------------------------------------
// GSM 7-bit extension characters.
// ---------------------------------------------------------------------------

func TestEncodeGSM7_ExtensionChars(t *testing.T) {
	// Each extension char costs 2 septets (escape + char).
	text := "{}[]~\\|^€"
	packed, septets, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7(%q) error: %v", text, err)
	}
	// 9 extension chars × 2 septets = 18 septets.
	wantSeptets := 18
	if septets != wantSeptets {
		t.Errorf("septets = %d, want %d", septets, wantSeptets)
	}

	decoded, err := DecodeGSM7(packed, septets)
	if err != nil {
		t.Fatalf("DecodeGSM7 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

// ---------------------------------------------------------------------------
// GSM 7-bit packing: 8 septets = 7 octets.
// ---------------------------------------------------------------------------

func TestEncodeGSM7_PackedSize(t *testing.T) {
	text := "AAAAAAAA" // 8 septets
	packed, septets, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7(%q) error: %v", text, err)
	}
	if septets != 8 {
		t.Errorf("septets = %d, want 8", septets)
	}
	if len(packed) != 7 {
		t.Errorf("packed len = %d, want 7 (8 septets × 7 bits = 56 bits = 7 bytes)", len(packed))
	}

	decoded, err := DecodeGSM7(packed, septets)
	if err != nil {
		t.Fatalf("DecodeGSM7 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeGSM7_PackedSizeVaried(t *testing.T) {
	tests := []struct {
		septets   int
		wantBytes int
	}{
		{1, 1},
		{7, 7},   // 49 bits → 7 bytes
		{8, 7},   // 56 bits → 7 bytes
		{9, 8},   // 63 bits → 8 bytes
		{16, 14}, // 112 bits → 14 bytes
	}
	for _, tt := range tests {
		text := strings.Repeat("A", tt.septets)
		packed, septets, err := EncodeGSM7(text)
		if err != nil {
			t.Fatalf("EncodeGSM7(%d A's) error: %v", tt.septets, err)
		}
		if septets != tt.septets {
			t.Errorf("septets = %d, want %d", septets, tt.septets)
		}
		if len(packed) != tt.wantBytes {
			t.Errorf("%d septets: packed len = %d, want %d", tt.septets, len(packed), tt.wantBytes)
		}
	}
}

// ---------------------------------------------------------------------------
// GSM 7-bit error on non-GSM character.
// ---------------------------------------------------------------------------

func TestEncodeGSM7_ErrorNonGSM(t *testing.T) {
	// Chinese character is not in GSM 7-bit.
	_, _, err := EncodeGSM7("\u4e16") // 世
	if err == nil {
		t.Fatal("expected error for non-GSM character, got nil")
	}
}

// ---------------------------------------------------------------------------
// UCS-2 encode/decode.
// ---------------------------------------------------------------------------

func TestEncodeUCS2_ASCII(t *testing.T) {
	text := "Hello"
	encoded, err := EncodeUCS2(text)
	if err != nil {
		t.Fatalf("EncodeUCS2(%q) error: %v", text, err)
	}
	// 5 chars × 2 bytes = 10 bytes.
	if len(encoded) != 10 {
		t.Errorf("encoded len = %d, want 10", len(encoded))
	}

	decoded, err := DecodeUCS2(encoded)
	if err != nil {
		t.Fatalf("DecodeUCS2 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeUCS2_AccentedChars(t *testing.T) {
	text := "\u00E9\u00E8\u00EA\u00EB" // éèêë
	encoded, err := EncodeUCS2(text)
	if err != nil {
		t.Fatalf("EncodeUCS2(%q) error: %v", text, err)
	}

	decoded, err := DecodeUCS2(encoded)
	if err != nil {
		t.Fatalf("DecodeUCS2 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeUCS2_CJK(t *testing.T) {
	text := "\u4e16\u754c" // 世界
	encoded, err := EncodeUCS2(text)
	if err != nil {
		t.Fatalf("EncodeUCS2(%q) error: %v", text, err)
	}

	decoded, err := DecodeUCS2(encoded)
	if err != nil {
		t.Fatalf("DecodeUCS2 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeUCS2_Emoji(t *testing.T) {
	text := "\U0001F600" // 😀 (surrogate pair in UTF-16)
	encoded, err := EncodeUCS2(text)
	if err != nil {
		t.Fatalf("EncodeUCS2(%q) error: %v", text, err)
	}
	// Surrogate pair = 4 bytes.
	if len(encoded) != 4 {
		t.Errorf("encoded len = %d, want 4 (surrogate pair)", len(encoded))
	}

	decoded, err := DecodeUCS2(encoded)
	if err != nil {
		t.Fatalf("DecodeUCS2 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestDecodeUCS2_OddLength(t *testing.T) {
	_, err := DecodeUCS2([]byte{0x00, 0x41, 0x00})
	if err == nil {
		t.Fatal("expected error for odd-length UCS2 data, got nil")
	}
}

// ---------------------------------------------------------------------------
// Latin-1 encode/decode.
// ---------------------------------------------------------------------------

func TestEncodeLatin1_Roundtrip(t *testing.T) {
	text := "caf\u00E9" // café
	encoded, err := EncodeLatin1(text)
	if err != nil {
		t.Fatalf("EncodeLatin1(%q) error: %v", text, err)
	}
	if len(encoded) != 4 {
		t.Errorf("encoded len = %d, want 4", len(encoded))
	}
	if encoded[3] != 0xE9 {
		t.Errorf("encoded[3] = 0x%02X, want 0xE9 (é)", encoded[3])
	}

	decoded, err := DecodeLatin1(encoded)
	if err != nil {
		t.Fatalf("DecodeLatin1 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeLatin1_ErrorBeyondFF(t *testing.T) {
	_, err := EncodeLatin1("\u0100") // Ā (Latin Extended-A, > 0xFF)
	if err == nil {
		t.Fatal("expected error for character > 0xFF, got nil")
	}
}

// ---------------------------------------------------------------------------
// IA5/ASCII encode/decode.
// ---------------------------------------------------------------------------

func TestEncodeIA5_Roundtrip(t *testing.T) {
	text := "Hello, World! 123"
	encoded, err := EncodeIA5(text)
	if err != nil {
		t.Fatalf("EncodeIA5(%q) error: %v", text, err)
	}
	if len(encoded) != len(text) {
		t.Errorf("encoded len = %d, want %d", len(encoded), len(text))
	}

	decoded, err := DecodeIA5(encoded)
	if err != nil {
		t.Fatalf("DecodeIA5 error: %v", err)
	}
	if decoded != text {
		t.Errorf("roundtrip: got %q, want %q", decoded, text)
	}
}

func TestEncodeIA5_ErrorBeyond7F(t *testing.T) {
	_, err := EncodeIA5("\u00E9") // é (> 0x7F)
	if err == nil {
		t.Fatal("expected error for character > 0x7F, got nil")
	}
}

// ---------------------------------------------------------------------------
// DetectCoding.
// ---------------------------------------------------------------------------

func TestDetectCoding_PureASCII(t *testing.T) {
	if got := DetectCoding("Hello World"); got != DataCodingDefault {
		t.Errorf("DetectCoding(ASCII) = 0x%02X, want 0x%02X (DataCodingDefault)", got, DataCodingDefault)
	}
}

func TestDetectCoding_GSMSpecialEuro(t *testing.T) {
	// € is in the GSM extension table, so should still be GSM 7-bit.
	if got := DetectCoding("Price: 100€"); got != DataCodingDefault {
		t.Errorf("DetectCoding(€) = 0x%02X, want 0x%02X (DataCodingDefault)", got, DataCodingDefault)
	}
}

func TestDetectCoding_NonGSM(t *testing.T) {
	// Chinese character is not GSM, should detect UCS2.
	if got := DetectCoding("\u4e16\u754c"); got != DataCodingUCS2 {
		t.Errorf("DetectCoding(CJK) = 0x%02X, want 0x%02X (DataCodingUCS2)", got, DataCodingUCS2)
	}
}

func TestDetectCoding_Emoji(t *testing.T) {
	if got := DetectCoding("Hi \U0001F600"); got != DataCodingUCS2 {
		t.Errorf("DetectCoding(emoji) = 0x%02X, want 0x%02X (DataCodingUCS2)", got, DataCodingUCS2)
	}
}

// ---------------------------------------------------------------------------
// Encode/Decode dispatch.
// ---------------------------------------------------------------------------

func TestEncode_GSM7Dispatch(t *testing.T) {
	text := "Hello"
	encoded, err := Encode(text, DataCodingDefault)
	if err != nil {
		t.Fatalf("Encode(GSM7) error: %v", err)
	}
	// Compare with direct EncodeGSM7.
	direct, _, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7 error: %v", err)
	}
	if len(encoded) != len(direct) {
		t.Errorf("Encode(GSM7) len = %d, EncodeGSM7 len = %d", len(encoded), len(direct))
	}
	for i := range encoded {
		if encoded[i] != direct[i] {
			t.Errorf("byte %d: Encode=0x%02X, EncodeGSM7=0x%02X", i, encoded[i], direct[i])
		}
	}
}

func TestEncode_UCS2Dispatch(t *testing.T) {
	text := "\u4e16\u754c"
	encoded, err := Encode(text, DataCodingUCS2)
	if err != nil {
		t.Fatalf("Encode(UCS2) error: %v", err)
	}
	direct, err := EncodeUCS2(text)
	if err != nil {
		t.Fatalf("EncodeUCS2 error: %v", err)
	}
	if len(encoded) != len(direct) {
		t.Errorf("Encode(UCS2) len = %d, EncodeUCS2 len = %d", len(encoded), len(direct))
	}
	for i := range encoded {
		if encoded[i] != direct[i] {
			t.Errorf("byte %d: Encode=0x%02X, EncodeUCS2=0x%02X", i, encoded[i], direct[i])
		}
	}
}

func TestDecode_GSM7Dispatch(t *testing.T) {
	text := "Hello"
	packed, septets, err := EncodeGSM7(text)
	if err != nil {
		t.Fatalf("EncodeGSM7 error: %v", err)
	}
	_ = septets

	decoded, err := Decode(packed, DataCodingDefault)
	if err != nil {
		t.Fatalf("Decode(GSM7) error: %v", err)
	}
	if decoded != text {
		t.Errorf("Decode(GSM7) = %q, want %q", decoded, text)
	}
}

func TestDecode_UCS2Dispatch(t *testing.T) {
	text := "\u4e16\u754c"
	encoded, err := EncodeUCS2(text)
	if err != nil {
		t.Fatalf("EncodeUCS2 error: %v", err)
	}

	decoded, err := Decode(encoded, DataCodingUCS2)
	if err != nil {
		t.Fatalf("Decode(UCS2) error: %v", err)
	}
	if decoded != text {
		t.Errorf("Decode(UCS2) = %q, want %q", decoded, text)
	}
}

func TestEncode_UnsupportedCoding(t *testing.T) {
	_, err := Encode("test", 0xFF)
	if err == nil {
		t.Fatal("expected error for unsupported coding, got nil")
	}
}

func TestDecode_UnsupportedCoding(t *testing.T) {
	_, err := Decode([]byte("test"), 0xFF)
	if err == nil {
		t.Fatal("expected error for unsupported coding, got nil")
	}
}
