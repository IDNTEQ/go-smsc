package smpp

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Data coding scheme constants (SMPP data_coding field values).
// ---------------------------------------------------------------------------

const (
	DataCodingDefault byte = 0x00 // GSM 7-bit default alphabet
	DataCodingIA5     byte = 0x01 // IA5 (CCITT T.50)/ASCII
	DataCodingLatin1  byte = 0x03 // Latin 1 (ISO-8859-1)
	DataCodingBinary  byte = 0x04 // Octet unspecified (8-bit binary)
	DataCodingUCS2    byte = 0x08 // UCS2 (ISO/IEC-10646) / UTF-16BE
)

// ---------------------------------------------------------------------------
// GSM 7-bit default alphabet (3GPP TS 23.038).
// ---------------------------------------------------------------------------

// gsm7Default maps GSM 7-bit code-point → Unicode rune.
// Index is the GSM 7-bit value (0x00–0x7F).
var gsm7Default = [128]rune{
	'@', '\u00A3', '$', '\u00A5', '\u00E8', '\u00E9', '\u00F9', '\u00EC',
	'\u00F2', '\u00C7', '\n', '\u00D8', '\u00F8', '\r', '\u00C5', '\u00E5',
	'\u0394', '_', '\u03A6', '\u0393', '\u039B', '\u03A9', '\u03A0', '\u03A8',
	'\u03A3', '\u0398', '\u039E', '\x1B', '\u00C6', '\u00E6', '\u00DF', '\u00C9',
	' ', '!', '"', '#', '\u00A4', '%', '&', '\'',
	'(', ')', '*', '+', ',', '-', '.', '/',
	'0', '1', '2', '3', '4', '5', '6', '7',
	'8', '9', ':', ';', '<', '=', '>', '?',
	'\u00A1', 'A', 'B', 'C', 'D', 'E', 'F', 'G',
	'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O',
	'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W',
	'X', 'Y', 'Z', '\u00C4', '\u00D6', '\u00D1', '\u00DC', '\u00A7',
	'\u00BF', 'a', 'b', 'c', 'd', 'e', 'f', 'g',
	'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o',
	'p', 'q', 'r', 's', 't', 'u', 'v', 'w',
	'x', 'y', 'z', '\u00E4', '\u00F6', '\u00F1', '\u00FC', '\u00E0',
}

// unicodeToGSM7 maps Unicode rune → GSM 7-bit value for the basic table.
var unicodeToGSM7 map[rune]byte

// gsm7Extension maps GSM 7-bit extension code-point → Unicode rune.
// These are accessed via escape character 0x1B.
var gsm7Extension = map[byte]rune{
	0x0A: '\x0C', // form feed
	0x14: '^',
	0x28: '{',
	0x29: '}',
	0x2F: '\\',
	0x3C: '[',
	0x3D: '~',
	0x3E: ']',
	0x40: '|',
	0x65: '\u20AC', // €
}

// unicodeToGSM7Ext maps Unicode rune → GSM 7-bit extension value.
var unicodeToGSM7Ext map[rune]byte

func init() {
	unicodeToGSM7 = make(map[rune]byte, 128)
	for i, r := range gsm7Default {
		if i == 0x1B {
			continue // escape character, not a printable char
		}
		unicodeToGSM7[r] = byte(i)
	}

	unicodeToGSM7Ext = make(map[rune]byte, len(gsm7Extension))
	for code, r := range gsm7Extension {
		unicodeToGSM7Ext[r] = code
	}
}

// isGSM7 reports whether r can be encoded in the GSM 7-bit alphabet
// (basic or extension table).
func isGSM7(r rune) bool {
	if _, ok := unicodeToGSM7[r]; ok {
		return true
	}
	_, ok := unicodeToGSM7Ext[r]
	return ok
}

// gsm7Len returns the number of septets required to encode r, or -1 if
// the rune is not in the GSM 7-bit alphabet.
func gsm7Len(r rune) int {
	if _, ok := unicodeToGSM7[r]; ok {
		return 1
	}
	if _, ok := unicodeToGSM7Ext[r]; ok {
		return 2 // escape + char
	}
	return -1
}

// ---------------------------------------------------------------------------
// GSM 7-bit encode/decode.
// ---------------------------------------------------------------------------

// EncodeGSM7 converts a UTF-8 string to packed GSM 7-bit encoding per
// 3GPP TS 23.038. Returns packed bytes and the number of septets.
func EncodeGSM7(text string) ([]byte, int, error) {
	// First pass: convert to septets.
	septets := make([]byte, 0, len(text))
	for _, r := range text {
		if code, ok := unicodeToGSM7[r]; ok {
			septets = append(septets, code)
		} else if ext, ok := unicodeToGSM7Ext[r]; ok {
			septets = append(septets, 0x1B, ext)
		} else {
			return nil, 0, fmt.Errorf("character %U (%c) cannot be encoded in GSM 7-bit alphabet", r, r)
		}
	}

	numSeptets := len(septets)

	// Pack septets into octets: each septet is 7 bits, packed LSB-first.
	packedLen := (numSeptets*7 + 7) / 8
	packed := make([]byte, packedLen)
	for i, s := range septets {
		bitOffset := i * 7
		bytePos := bitOffset / 8
		bitPos := bitOffset % 8
		packed[bytePos] |= s << uint(bitPos)
		if bitPos > 1 && bytePos+1 < len(packed) {
			packed[bytePos+1] |= s >> uint(8-bitPos)
		}
	}

	// When exactly 7 fill bits remain in the last byte, pad with a CR
	// character (GSM code 0x0D) to prevent decoders that ignore the septet
	// count from producing a spurious '@' (GSM code 0x00). This is the
	// convention used by most SMPP libraries (cloudhopper,
	// CursedHardware/go-smpp, etc.). Fewer than 7 fill bits cannot form a
	// complete septet, so zero-fill is fine.
	fillBits := packedLen*8 - numSeptets*7
	if fillBits == 7 && packedLen > 0 {
		startBit := numSeptets * 7
		cr := byte(0x0D) // carriage return in GSM 7-bit
		for i := range 7 {
			bitIdx := startBit + i
			bytePos := bitIdx / 8
			bitPos := uint(bitIdx % 8)
			if bytePos < packedLen {
				packed[bytePos] |= ((cr >> uint(i)) & 1) << bitPos
			}
		}
	}

	return packed, numSeptets, nil
}

// DecodeGSM7 unpacks GSM 7-bit packed data and converts back to a UTF-8
// string. numSeptets is the number of septets encoded in the data.
func DecodeGSM7(data []byte, numSeptets int) (string, error) {
	// Unpack septets from octets.
	septets := make([]byte, numSeptets)
	for i := range numSeptets {
		bitOffset := i * 7
		bytePos := bitOffset / 8
		bitPos := bitOffset % 8
		if bytePos >= len(data) {
			return "", fmt.Errorf("GSM7 data too short: need byte %d, have %d bytes", bytePos, len(data))
		}
		septets[i] = (data[bytePos] >> uint(bitPos)) & 0x7F
		if bitPos > 1 && bytePos+1 < len(data) {
			septets[i] |= (data[bytePos+1] << uint(8-bitPos)) & 0x7F
		}
	}

	// Convert septets to runes.
	var result []rune
	escape := false
	for _, s := range septets {
		if escape {
			if r, ok := gsm7Extension[s]; ok {
				result = append(result, r)
			} else {
				// Unknown extension, treat as space (per spec recommendation).
				result = append(result, ' ')
			}
			escape = false
			continue
		}
		if s == 0x1B {
			escape = true
			continue
		}
		result = append(result, gsm7Default[s])
	}

	return string(result), nil
}

// ---------------------------------------------------------------------------
// UCS-2 (UTF-16BE) encode/decode.
// ---------------------------------------------------------------------------

// EncodeUCS2 encodes a UTF-8 string to UTF-16BE bytes.
func EncodeUCS2(text string) ([]byte, error) {
	runes := []rune(text)
	u16 := utf16.Encode(runes)
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.BigEndian.PutUint16(buf[i*2:], v)
	}
	return buf, nil
}

// DecodeUCS2 decodes UTF-16BE bytes to a UTF-8 string.
func DecodeUCS2(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("UCS2 data has odd length: %d", len(data))
	}
	u16 := make([]uint16, len(data)/2)
	for i := range u16 {
		u16[i] = binary.BigEndian.Uint16(data[i*2:])
	}
	runes := utf16.Decode(u16)
	return string(runes), nil
}

// ---------------------------------------------------------------------------
// Latin-1 (ISO-8859-1) encode/decode.
// ---------------------------------------------------------------------------

// EncodeLatin1 encodes a UTF-8 string to ISO-8859-1 bytes.
// Returns an error if any character is outside the Latin-1 range (> 0xFF).
func EncodeLatin1(text string) ([]byte, error) {
	buf := make([]byte, 0, len(text))
	for _, r := range text {
		if r > 0xFF {
			return nil, fmt.Errorf("character %U (%c) cannot be encoded in Latin-1", r, r)
		}
		buf = append(buf, byte(r))
	}
	return buf, nil
}

// DecodeLatin1 decodes ISO-8859-1 bytes to a UTF-8 string.
func DecodeLatin1(data []byte) (string, error) {
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return string(runes), nil
}

// ---------------------------------------------------------------------------
// IA5/ASCII encode/decode.
// ---------------------------------------------------------------------------

// EncodeIA5 encodes a UTF-8 string to IA5 (ASCII) bytes.
// Returns an error if any character is outside the ASCII range (> 0x7F).
func EncodeIA5(text string) ([]byte, error) {
	buf := make([]byte, 0, len(text))
	for _, r := range text {
		if r > 0x7F {
			return nil, fmt.Errorf("character %U (%c) cannot be encoded in IA5/ASCII", r, r)
		}
		buf = append(buf, byte(r))
	}
	return buf, nil
}

// DecodeIA5 decodes IA5 (ASCII) bytes to a UTF-8 string.
func DecodeIA5(data []byte) (string, error) {
	// All bytes 0x00–0x7F are valid ASCII and valid UTF-8.
	return string(data), nil
}

// ---------------------------------------------------------------------------
// Generic encode/decode dispatch.
// ---------------------------------------------------------------------------

// Encode converts a UTF-8 string to the specified SMPP data coding scheme.
func Encode(text string, coding byte) ([]byte, error) {
	switch coding {
	case DataCodingDefault:
		packed, _, err := EncodeGSM7(text)
		return packed, err
	case DataCodingIA5:
		return EncodeIA5(text)
	case DataCodingLatin1:
		return EncodeLatin1(text)
	case DataCodingBinary:
		return []byte(text), nil
	case DataCodingUCS2:
		return EncodeUCS2(text)
	default:
		return nil, fmt.Errorf("unsupported data coding: 0x%02X", coding)
	}
}

// Decode converts encoded bytes back to a UTF-8 string.
func Decode(data []byte, coding byte) (string, error) {
	switch coding {
	case DataCodingDefault:
		// For generic Decode we need to know the number of septets.
		// Estimate from packed data: numSeptets = len(data) * 8 / 7.
		numSeptets := len(data) * 8 / 7
		return DecodeGSM7(data, numSeptets)
	case DataCodingIA5:
		return DecodeIA5(data)
	case DataCodingLatin1:
		return DecodeLatin1(data)
	case DataCodingBinary:
		return string(data), nil
	case DataCodingUCS2:
		return DecodeUCS2(data)
	default:
		return "", fmt.Errorf("unsupported data coding: 0x%02X", coding)
	}
}

// DetectCoding returns the optimal data coding for the given UTF-8 text.
// Returns DataCodingDefault if all characters fit GSM 7-bit, otherwise
// DataCodingUCS2.
func DetectCoding(text string) byte {
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !isGSM7(r) {
			return DataCodingUCS2
		}
		i += size
	}
	return DataCodingDefault
}
