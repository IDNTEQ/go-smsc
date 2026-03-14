package smpp

import (
	"fmt"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Segment represents one part of a segmented message.
// ---------------------------------------------------------------------------

// Segment represents one part of a segmented message.
type Segment struct {
	Data       []byte
	RefNum     uint16 // shared across all segments of the same message
	SeqNum     byte   // 1-based segment number
	TotalParts byte   // total segments in the message
	DataCoding byte
}

// ---------------------------------------------------------------------------
// SplitMethod determines how segmentation metadata is carried.
// ---------------------------------------------------------------------------

// SplitMethod determines how segmentation metadata is carried.
type SplitMethod int

const (
	// SplitSAR uses SAR TLVs (sar_msg_ref_num, sar_total_segments,
	// sar_segment_seqnum).
	SplitSAR SplitMethod = iota
	// SplitUDH prepends a User Data Header to each short_message.
	SplitUDH
)

// ---------------------------------------------------------------------------
// Segment reference counter.
// ---------------------------------------------------------------------------

var segmentRefCounter atomic.Uint32

func nextSegmentRef() uint16 {
	return uint16(segmentRefCounter.Add(1) & 0xFFFF)
}

// ---------------------------------------------------------------------------
// Character counting helpers.
// ---------------------------------------------------------------------------

// gsm7CharCount returns the total number of GSM 7-bit septets needed to
// encode text. Returns -1 if any character is not in the GSM 7-bit alphabet.
func gsm7CharCount(text string) int {
	count := 0
	for _, r := range text {
		n := gsm7Len(r)
		if n < 0 {
			return -1
		}
		count += n
	}
	return count
}

// ---------------------------------------------------------------------------
// Message segmentation limits.
// ---------------------------------------------------------------------------

const (
	// GSM 7-bit limits (in septets).
	gsm7SingleMax  = 160
	gsm7SegmentMax = 153 // 160 - 7 (UDH takes 7 septets of space)

	// UCS-2 limits (in code units / characters).
	ucs2SingleMax  = 70
	ucs2SegmentMax = 67 // 70 - 3 (UDH = 6 bytes = 3 UCS-2 chars)
)

// UDH header size in bytes for concatenated SM.
const udhConcatLen = 6

// ---------------------------------------------------------------------------
// SplitMessage splits a UTF-8 text message into segments.
// ---------------------------------------------------------------------------

// SplitMessage splits a UTF-8 text message into segments.
// coding determines the encoding. method determines SAR vs UDH.
// For GSM 7-bit: max 160 chars per single message, 153 chars per segment
// (UDH takes 7 chars worth of space in a 160-septet payload).
// For UCS-2: max 70 chars per single message, 67 chars per segment
// (UDH takes 6 bytes = 3 chars).
func SplitMessage(text string, coding byte, method SplitMethod) ([]Segment, error) {
	if len(text) == 0 {
		return nil, fmt.Errorf("empty message text")
	}

	switch coding {
	case DataCodingDefault:
		return splitGSM7(text, method)
	case DataCodingUCS2:
		return splitUCS2(text, method)
	default:
		return nil, fmt.Errorf("segmentation not supported for data coding 0x%02X", coding)
	}
}

// splitGSM7 segments a GSM 7-bit message.
func splitGSM7(text string, method SplitMethod) ([]Segment, error) {
	totalSeptets := gsm7CharCount(text)
	if totalSeptets < 0 {
		return nil, fmt.Errorf("text contains characters not in GSM 7-bit alphabet")
	}

	// Single segment — fits within 160 septets.
	if totalSeptets <= gsm7SingleMax {
		packed, _, err := EncodeGSM7(text)
		if err != nil {
			return nil, err
		}
		return []Segment{{
			Data:       packed,
			RefNum:     0,
			SeqNum:     1,
			TotalParts: 1,
			DataCoding: DataCodingDefault,
		}}, nil
	}

	// Multi-segment — split on character boundaries respecting septet counts.
	parts := splitTextBySeptets(text, gsm7SegmentMax)
	refNum := nextSegmentRef()
	totalParts := byte(len(parts))

	segments := make([]Segment, len(parts))
	for i, part := range parts {
		packed, _, err := EncodeGSM7(part)
		if err != nil {
			return nil, err
		}

		var data []byte
		if method == SplitUDH {
			data = prependUDH(packed, refNum, totalParts, byte(i+1))
		} else {
			data = packed
		}

		segments[i] = Segment{
			Data:       data,
			RefNum:     refNum,
			SeqNum:     byte(i + 1),
			TotalParts: totalParts,
			DataCoding: DataCodingDefault,
		}
	}

	return segments, nil
}

// splitTextBySeptets splits text into parts where each part uses at most
// maxSeptets GSM 7-bit septets.
func splitTextBySeptets(text string, maxSeptets int) []string {
	var parts []string
	var current []rune
	currentSeptets := 0

	for _, r := range text {
		n := gsm7Len(r)
		if currentSeptets+n > maxSeptets {
			parts = append(parts, string(current))
			current = current[:0]
			currentSeptets = 0
		}
		current = append(current, r)
		currentSeptets += n
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}

	return parts
}

// splitUCS2 segments a UCS-2 message.
func splitUCS2(text string, method SplitMethod) ([]Segment, error) {
	runes := []rune(text)
	charCount := len(runes)

	// Single segment — fits within 70 characters.
	if charCount <= ucs2SingleMax {
		encoded, err := EncodeUCS2(text)
		if err != nil {
			return nil, err
		}
		return []Segment{{
			Data:       encoded,
			RefNum:     0,
			SeqNum:     1,
			TotalParts: 1,
			DataCoding: DataCodingUCS2,
		}}, nil
	}

	// Multi-segment — split on character boundaries.
	parts := splitRuneChunks(runes, ucs2SegmentMax)
	refNum := nextSegmentRef()
	totalParts := byte(len(parts))

	segments := make([]Segment, len(parts))
	for i, part := range parts {
		encoded, err := EncodeUCS2(string(part))
		if err != nil {
			return nil, err
		}

		var data []byte
		if method == SplitUDH {
			data = prependUDH(encoded, refNum, totalParts, byte(i+1))
		} else {
			data = encoded
		}

		segments[i] = Segment{
			Data:       data,
			RefNum:     refNum,
			SeqNum:     byte(i + 1),
			TotalParts: totalParts,
			DataCoding: DataCodingUCS2,
		}
	}

	return segments, nil
}

// splitRuneChunks splits runes into chunks of at most chunkSize runes.
func splitRuneChunks(runes []rune, chunkSize int) [][]rune {
	var chunks [][]rune
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, runes[i:end])
	}
	return chunks
}

// prependUDH creates a new byte slice with the 6-byte concatenation UDH
// prepended to payload.
//
//	0x05        — UDH length (5 bytes follow)
//	0x00        — Concatenated SM IEI
//	0x03        — IEI data length
//	refNum(1)   — Reference number (low byte)
//	totalParts  — Total parts
//	seqNum      — Sequence number
func prependUDH(payload []byte, refNum uint16, totalParts, seqNum byte) []byte {
	udh := []byte{
		0x05,              // UDH length
		0x00,              // Concatenated SM IEI
		0x03,              // IEI data length
		byte(refNum),      // reference number (low byte)
		totalParts,        // total parts
		seqNum,            // sequence number
	}
	result := make([]byte, len(udh)+len(payload))
	copy(result, udh)
	copy(result[len(udh):], payload)
	return result
}
