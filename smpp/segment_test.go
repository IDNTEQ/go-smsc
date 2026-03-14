package smpp

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// GSM 7-bit segmentation.
// ---------------------------------------------------------------------------

func TestSplitMessage_GSM7Short(t *testing.T) {
	// 160 chars → 1 segment.
	text := strings.Repeat("A", 160)
	segs, err := SplitMessage(text, DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
	if segs[0].TotalParts != 1 {
		t.Errorf("TotalParts = %d, want 1", segs[0].TotalParts)
	}
	if segs[0].SeqNum != 1 {
		t.Errorf("SeqNum = %d, want 1", segs[0].SeqNum)
	}
}

func TestSplitMessage_GSM7_161Chars(t *testing.T) {
	// 161 chars → 2 segments (153 + 8).
	text := strings.Repeat("A", 161)
	segs, err := SplitMessage(text, DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
	if segs[0].TotalParts != 2 {
		t.Errorf("TotalParts = %d, want 2", segs[0].TotalParts)
	}
	if segs[0].SeqNum != 1 {
		t.Errorf("seg[0].SeqNum = %d, want 1", segs[0].SeqNum)
	}
	if segs[1].SeqNum != 2 {
		t.Errorf("seg[1].SeqNum = %d, want 2", segs[1].SeqNum)
	}
}

func TestSplitMessage_GSM7_306Chars(t *testing.T) {
	// 306 chars = 2 × 153 → 2 segments.
	text := strings.Repeat("B", 306)
	segs, err := SplitMessage(text, DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
}

func TestSplitMessage_GSM7_307Chars(t *testing.T) {
	// 307 chars > 2 × 153 → 3 segments.
	text := strings.Repeat("C", 307)
	segs, err := SplitMessage(text, DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("segments = %d, want 3", len(segs))
	}
	if segs[0].TotalParts != 3 {
		t.Errorf("TotalParts = %d, want 3", segs[0].TotalParts)
	}
	if segs[2].SeqNum != 3 {
		t.Errorf("seg[2].SeqNum = %d, want 3", segs[2].SeqNum)
	}
}

// ---------------------------------------------------------------------------
// UCS-2 segmentation.
// ---------------------------------------------------------------------------

func TestSplitMessage_UCS2Short(t *testing.T) {
	// 70 UCS-2 chars → 1 segment.
	text := strings.Repeat("\u4e16", 70)
	segs, err := SplitMessage(text, DataCodingUCS2, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
}

func TestSplitMessage_UCS2_71Chars(t *testing.T) {
	// 71 chars → 2 segments (67 + 4).
	text := strings.Repeat("\u4e16", 71)
	segs, err := SplitMessage(text, DataCodingUCS2, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
	if segs[0].TotalParts != 2 {
		t.Errorf("TotalParts = %d, want 2", segs[0].TotalParts)
	}
}

// ---------------------------------------------------------------------------
// SplitUDH: verify UDH header.
// ---------------------------------------------------------------------------

func TestSplitMessage_UDH_GSM7(t *testing.T) {
	text := strings.Repeat("D", 161) // 2 segments
	segs, err := SplitMessage(text, DataCodingDefault, SplitUDH)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}

	for i, seg := range segs {
		if len(seg.Data) < udhConcatLen {
			t.Fatalf("seg[%d].Data too short: %d bytes", i, len(seg.Data))
		}
		// UDH header bytes.
		if seg.Data[0] != 0x05 {
			t.Errorf("seg[%d] UDH[0] = 0x%02X, want 0x05", i, seg.Data[0])
		}
		if seg.Data[1] != 0x00 {
			t.Errorf("seg[%d] UDH[1] = 0x%02X, want 0x00", i, seg.Data[1])
		}
		if seg.Data[2] != 0x03 {
			t.Errorf("seg[%d] UDH[2] = 0x%02X, want 0x03", i, seg.Data[2])
		}
		// UDH[3] = refNum (low byte) — just check it's consistent.
		if segs[0].Data[3] != segs[1].Data[3] {
			t.Errorf("refNum mismatch between segments: 0x%02X vs 0x%02X", segs[0].Data[3], segs[1].Data[3])
		}
		// UDH[4] = totalParts.
		if seg.Data[4] != 2 {
			t.Errorf("seg[%d] UDH totalParts = %d, want 2", i, seg.Data[4])
		}
		// UDH[5] = seqNum.
		if seg.Data[5] != byte(i+1) {
			t.Errorf("seg[%d] UDH seqNum = %d, want %d", i, seg.Data[5], i+1)
		}
	}
}

func TestSplitMessage_UDH_UCS2(t *testing.T) {
	text := strings.Repeat("\u4e16", 71) // 2 segments
	segs, err := SplitMessage(text, DataCodingUCS2, SplitUDH)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}

	for i, seg := range segs {
		if len(seg.Data) < udhConcatLen {
			t.Fatalf("seg[%d].Data too short: %d bytes", i, len(seg.Data))
		}
		if seg.Data[0] != 0x05 {
			t.Errorf("seg[%d] UDH[0] = 0x%02X, want 0x05", i, seg.Data[0])
		}
		if seg.Data[1] != 0x00 {
			t.Errorf("seg[%d] UDH[1] = 0x%02X, want 0x00", i, seg.Data[1])
		}
		if seg.Data[2] != 0x03 {
			t.Errorf("seg[%d] UDH[2] = 0x%02X, want 0x03", i, seg.Data[2])
		}
	}
}

// ---------------------------------------------------------------------------
// SplitSAR: verify no UDH, correct metadata.
// ---------------------------------------------------------------------------

func TestSplitMessage_SAR_NoUDH(t *testing.T) {
	text := strings.Repeat("E", 161) // 2 segments
	segs, err := SplitMessage(text, DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}

	// SAR segments must not have UDH header — first byte should NOT be 0x05
	// (unless the GSM7 packed data happens to start with that, which for 'E'
	// it won't). More robustly, SAR data should be pure encoded message.
	// For 153 'E' chars encoded in GSM7, packed size = ceil(153*7/8) = 134 bytes.
	wantLen := (153*7 + 7) / 8
	if len(segs[0].Data) != wantLen {
		t.Errorf("seg[0].Data len = %d, want %d (pure GSM7 packed, no UDH)", len(segs[0].Data), wantLen)
	}

	// Verify RefNum, SeqNum, TotalParts.
	if segs[0].RefNum != segs[1].RefNum {
		t.Errorf("RefNum mismatch: %d vs %d", segs[0].RefNum, segs[1].RefNum)
	}
	if segs[0].RefNum == 0 {
		t.Error("multi-segment RefNum should be non-zero")
	}
	if segs[0].SeqNum != 1 || segs[1].SeqNum != 2 {
		t.Errorf("SeqNum: got %d,%d, want 1,2", segs[0].SeqNum, segs[1].SeqNum)
	}
	if segs[0].TotalParts != 2 || segs[1].TotalParts != 2 {
		t.Errorf("TotalParts: got %d,%d, want 2,2", segs[0].TotalParts, segs[1].TotalParts)
	}
}

// ---------------------------------------------------------------------------
// Single character message.
// ---------------------------------------------------------------------------

func TestSplitMessage_SingleChar(t *testing.T) {
	segs, err := SplitMessage("A", DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
	if segs[0].TotalParts != 1 {
		t.Errorf("TotalParts = %d, want 1", segs[0].TotalParts)
	}
}

// ---------------------------------------------------------------------------
// Empty text.
// ---------------------------------------------------------------------------

func TestSplitMessage_Empty(t *testing.T) {
	_, err := SplitMessage("", DataCodingDefault, SplitSAR)
	if err == nil {
		t.Fatal("expected error for empty message, got nil")
	}
}

// ---------------------------------------------------------------------------
// Unsupported coding.
// ---------------------------------------------------------------------------

func TestSplitMessage_UnsupportedCoding(t *testing.T) {
	_, err := SplitMessage("test", DataCodingBinary, SplitSAR)
	if err == nil {
		t.Fatal("expected error for unsupported coding, got nil")
	}
}

// ---------------------------------------------------------------------------
// DataCoding field on segments.
// ---------------------------------------------------------------------------

func TestSplitMessage_DataCodingField(t *testing.T) {
	segsGSM, err := SplitMessage("Hello", DataCodingDefault, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage(GSM7) error: %v", err)
	}
	if segsGSM[0].DataCoding != DataCodingDefault {
		t.Errorf("GSM7 segment DataCoding = 0x%02X, want 0x%02X", segsGSM[0].DataCoding, DataCodingDefault)
	}

	segsUCS, err := SplitMessage("\u4e16", DataCodingUCS2, SplitSAR)
	if err != nil {
		t.Fatalf("SplitMessage(UCS2) error: %v", err)
	}
	if segsUCS[0].DataCoding != DataCodingUCS2 {
		t.Errorf("UCS2 segment DataCoding = 0x%02X, want 0x%02X", segsUCS[0].DataCoding, DataCodingUCS2)
	}
}
