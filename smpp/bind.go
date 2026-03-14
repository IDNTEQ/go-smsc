package smpp

import "bytes"

// BindMode represents the SMPP session type.
type BindMode uint8

const (
	BindTransceiver  BindMode = iota // default
	BindTransmitter
	BindReceiver
)

// EncodeBind creates the body for a bind PDU of the given mode.
// interfaceVersion should be 0x34 for SMPP 3.4 or 0x50 for SMPP 5.0.
func EncodeBind(mode BindMode, systemID, password, systemType string, interfaceVersion byte) []byte {
	_ = mode // mode only affects command ID selection, not body encoding
	var buf bytes.Buffer
	writeCString(&buf, systemID)
	writeCString(&buf, password)
	writeCString(&buf, systemType)
	buf.WriteByte(interfaceVersion) // interface_version
	buf.WriteByte(0x00)             // addr_ton
	buf.WriteByte(0x00)             // addr_npi
	writeCString(&buf, "")          // address_range
	return buf.Bytes()
}

// bindCommandID returns the command ID for the given bind mode.
func bindCommandID(mode BindMode) uint32 {
	switch mode {
	case BindTransmitter:
		return CmdBindTransmitter
	case BindReceiver:
		return CmdBindReceiver
	default:
		return CmdBindTransceiver
	}
}

// bindRespCommandID returns the expected response command ID.
func bindRespCommandID(mode BindMode) uint32 {
	switch mode {
	case BindTransmitter:
		return CmdBindTransmitterResp
	case BindReceiver:
		return CmdBindReceiverResp
	default:
		return CmdBindTransceiverResp
	}
}

// ParseBindResp parses a bind response body.
// Returns the system_id from the SMSC and extracts TLVs (including
// sc_interface_version). The commandID parameter is used to determine
// the mandatory body length for TLV extraction.
func ParseBindResp(commandID uint32, body []byte) (systemID string, tlvs TLVSet) {
	if len(body) == 0 {
		return "", nil
	}
	systemID, offset := readCString(body, 0)

	// Any bytes after the system_id C-string are TLVs.
	if offset < len(body) {
		var err error
		tlvs, err = DecodeTLVs(body[offset:])
		if err != nil {
			// Malformed TLVs — return what we have without TLVs.
			return systemID, nil
		}
	}
	return systemID, tlvs
}
