package smpp

import (
	"testing"
)

// =============================================================================
// EncodeBind tests
// =============================================================================

func TestEncodeBind_AllModes(t *testing.T) {
	tests := []struct {
		name             string
		mode             BindMode
		wantCmdID        uint32
		wantRespCmdID    uint32
		interfaceVersion byte
	}{
		{
			name:             "bind_transceiver",
			mode:             BindTransceiver,
			wantCmdID:        CmdBindTransceiver,
			wantRespCmdID:    CmdBindTransceiverResp,
			interfaceVersion: 0x34,
		},
		{
			name:             "bind_transmitter",
			mode:             BindTransmitter,
			wantCmdID:        CmdBindTransmitter,
			wantRespCmdID:    CmdBindTransmitterResp,
			interfaceVersion: 0x34,
		},
		{
			name:             "bind_receiver",
			mode:             BindReceiver,
			wantCmdID:        CmdBindReceiver,
			wantRespCmdID:    CmdBindReceiverResp,
			interfaceVersion: 0x34,
		},
		{
			name:             "bind_transceiver SMPP 5.0",
			mode:             BindTransceiver,
			wantCmdID:        CmdBindTransceiver,
			wantRespCmdID:    CmdBindTransceiverResp,
			interfaceVersion: 0x50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmdID := bindCommandID(tt.mode)
			if gotCmdID != tt.wantCmdID {
				t.Errorf("bindCommandID() = 0x%08X, want 0x%08X", gotCmdID, tt.wantCmdID)
			}

			gotRespCmdID := bindRespCommandID(tt.mode)
			if gotRespCmdID != tt.wantRespCmdID {
				t.Errorf("bindRespCommandID() = 0x%08X, want 0x%08X", gotRespCmdID, tt.wantRespCmdID)
			}

			body := EncodeBind(tt.mode, "test_user", "secret", "mytype", tt.interfaceVersion)
			if len(body) == 0 {
				t.Fatal("EncodeBind() returned empty body")
			}

			// Verify body fields by parsing.
			offset := 0
			systemID, offset := readCString(body, offset)
			if systemID != "test_user" {
				t.Errorf("system_id = %q, want %q", systemID, "test_user")
			}

			password, offset := readCString(body, offset)
			if password != "secret" {
				t.Errorf("password = %q, want %q", password, "secret")
			}

			systemType, offset := readCString(body, offset)
			if systemType != "mytype" {
				t.Errorf("system_type = %q, want %q", systemType, "mytype")
			}

			if offset >= len(body) {
				t.Fatal("body too short for interface_version")
			}
			if body[offset] != tt.interfaceVersion {
				t.Errorf("interface_version = 0x%02X, want 0x%02X", body[offset], tt.interfaceVersion)
			}
		})
	}
}

// =============================================================================
// ParseBindResp tests
// =============================================================================

func TestParseBindResp_WithoutTLVs(t *testing.T) {
	body := []byte("SMSC-01\x00")
	systemID, tlvs := ParseBindResp(CmdBindTransceiverResp, body)
	if systemID != "SMSC-01" {
		t.Errorf("systemID = %q, want %q", systemID, "SMSC-01")
	}
	if tlvs != nil {
		t.Errorf("expected nil TLVs, got %v", tlvs)
	}
}

func TestParseBindResp_WithSCInterfaceVersion(t *testing.T) {
	// Build body: system_id C-string + sc_interface_version TLV.
	systemIDBytes := []byte("SMSC-02\x00")
	tlvSet := make(TLVSet)
	tlvSet.SetUint8(TagSCInterfaceVersion, 0x50)
	tlvBytes := tlvSet.Encode()

	body := make([]byte, len(systemIDBytes)+len(tlvBytes))
	copy(body, systemIDBytes)
	copy(body[len(systemIDBytes):], tlvBytes)

	systemID, tlvs := ParseBindResp(CmdBindTransceiverResp, body)
	if systemID != "SMSC-02" {
		t.Errorf("systemID = %q, want %q", systemID, "SMSC-02")
	}
	if tlvs == nil {
		t.Fatal("expected non-nil TLVs")
	}
	ver, ok := tlvs.GetUint8(TagSCInterfaceVersion)
	if !ok || ver != 0x50 {
		t.Errorf("sc_interface_version = 0x%02X (ok=%v), want 0x50", ver, ok)
	}
}

func TestParseBindResp_EmptyBody(t *testing.T) {
	systemID, tlvs := ParseBindResp(CmdBindTransceiverResp, nil)
	if systemID != "" {
		t.Errorf("systemID = %q, want empty", systemID)
	}
	if tlvs != nil {
		t.Errorf("expected nil TLVs, got %v", tlvs)
	}
}

func TestParseBindResp_JustNull(t *testing.T) {
	body := []byte{0x00}
	systemID, tlvs := ParseBindResp(CmdBindReceiverResp, body)
	if systemID != "" {
		t.Errorf("systemID = %q, want empty", systemID)
	}
	if tlvs != nil {
		t.Errorf("expected nil TLVs, got %v", tlvs)
	}
}

// =============================================================================
// Roundtrip: EncodeBind -> decode body -> verify fields
// =============================================================================

func TestEncodeBind_Roundtrip(t *testing.T) {
	modes := []BindMode{BindTransceiver, BindTransmitter, BindReceiver}
	for _, mode := range modes {
		body := EncodeBind(mode, "myid", "mypass", "systype", 0x34)

		// Parse the body as if it were received.
		offset := 0
		sysID, offset := readCString(body, offset)
		pw, offset := readCString(body, offset)
		st, offset := readCString(body, offset)

		if sysID != "myid" {
			t.Errorf("mode %d: system_id = %q, want %q", mode, sysID, "myid")
		}
		if pw != "mypass" {
			t.Errorf("mode %d: password = %q, want %q", mode, pw, "mypass")
		}
		if st != "systype" {
			t.Errorf("mode %d: system_type = %q, want %q", mode, st, "systype")
		}
		if offset >= len(body) {
			t.Fatalf("mode %d: body too short", mode)
		}
		if body[offset] != 0x34 {
			t.Errorf("mode %d: interface_version = 0x%02X, want 0x34", mode, body[offset])
		}
		offset++ // interface_version
		if offset >= len(body) {
			t.Fatalf("mode %d: body too short for addr_ton", mode)
		}
		if body[offset] != 0x00 {
			t.Errorf("mode %d: addr_ton = 0x%02X, want 0x00", mode, body[offset])
		}
		offset++ // addr_ton
		if offset >= len(body) {
			t.Fatalf("mode %d: body too short for addr_npi", mode)
		}
		if body[offset] != 0x00 {
			t.Errorf("mode %d: addr_npi = 0x%02X, want 0x00", mode, body[offset])
		}
		offset++ // addr_npi
		addrRange, _ := readCString(body, offset)
		if addrRange != "" {
			t.Errorf("mode %d: address_range = %q, want empty", mode, addrRange)
		}
	}
}

// =============================================================================
// EncodeBindTransceiver wrapper compatibility
// =============================================================================

func TestEncodeBindTransceiver_WrapperCompatibility(t *testing.T) {
	// EncodeBindTransceiver should produce the same output as
	// EncodeBind(BindTransceiver, ..., 0x34).
	wrapper := EncodeBindTransceiver("user1", "pass1", "type1")
	direct := EncodeBind(BindTransceiver, "user1", "pass1", "type1", 0x34)

	if len(wrapper) != len(direct) {
		t.Fatalf("lengths differ: wrapper=%d, direct=%d", len(wrapper), len(direct))
	}
	for i := range wrapper {
		if wrapper[i] != direct[i] {
			t.Errorf("byte[%d] differs: wrapper=0x%02X, direct=0x%02X", i, wrapper[i], direct[i])
			break
		}
	}
}
