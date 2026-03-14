package smpp

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Tag identifies a TLV parameter in an SMPP PDU.
type Tag uint16

// SMPP 3.4 TLV tag constants (§5.3.2).
const (
	TagDestAddrSubunit          Tag = 0x0005
	TagDestNetworkType          Tag = 0x0006
	TagDestBearerType           Tag = 0x0007
	TagDestTelematicsID         Tag = 0x0008
	TagSourceAddrSubunit        Tag = 0x000D
	TagSourceNetworkType        Tag = 0x000E
	TagSourceBearerType         Tag = 0x000F
	TagSourceTelematicsID       Tag = 0x0010
	TagQOSTimeToLive            Tag = 0x0017
	TagPayloadType              Tag = 0x0019
	TagAdditionalStatusInfoText Tag = 0x001D
	TagReceiptedMessageID       Tag = 0x001E
	TagMSMsgWaitFacilities      Tag = 0x0030
	TagPrivacyIndicator         Tag = 0x0201
	TagSourcePort               Tag = 0x020A
	TagDestinationPort          Tag = 0x020B
	TagSarMsgRefNum             Tag = 0x020C
	TagLanguageIndicator        Tag = 0x020D
	TagSarTotalSegments         Tag = 0x020E
	TagSarSegmentSeqnum         Tag = 0x020F
	TagSCInterfaceVersion       Tag = 0x0210
	TagCallbackNumPresInd       Tag = 0x0302
	TagCallbackNumAtag          Tag = 0x0303
	TagNumberOfMessages         Tag = 0x0304
	TagCallbackNum              Tag = 0x0381
	TagDPFResult                Tag = 0x0420
	TagSetDPF                   Tag = 0x0421
	TagMSAvailabilityStatus     Tag = 0x0422
	TagNetworkErrorCode         Tag = 0x0423
	TagMessagePayload           Tag = 0x0424
	TagDeliveryFailureReason    Tag = 0x0425
	TagMoreMessagesToSend       Tag = 0x0426
	TagMessageState             Tag = 0x0427
	TagUserMessageReference     Tag = 0x0204
	TagUserResponseCode         Tag = 0x0205
	TagSourceSubaddress         Tag = 0x0202
	TagDestSubaddress           Tag = 0x0203
	TagUSSDServiceOp            Tag = 0x0501
	TagDisplayTime              Tag = 0x1201
	TagSMSSignal                Tag = 0x1203
	TagMSValidity               Tag = 0x1204
	TagAlertOnMessageDelivery   Tag = 0x130C
	TagITSReplyType             Tag = 0x1380
	TagITSSessionInfo           Tag = 0x1383
)

// SMPP 5.0 TLV tag constants.
const (
	TagCongestionState            Tag = 0x0428
	TagBroadcastChannelIndicator  Tag = 0x0600
	TagBroadcastContentType       Tag = 0x0601
	TagBroadcastContentTypeInfo   Tag = 0x0602
	TagBroadcastMessageClass      Tag = 0x0603
	TagBroadcastRepNum            Tag = 0x0604
	TagBroadcastFrequencyInterval Tag = 0x0605
	TagBroadcastAreaIdentifier    Tag = 0x0606
	TagBroadcastErrorStatus       Tag = 0x0607
	TagBroadcastAreaSuccess       Tag = 0x0608
	TagBroadcastEndTime           Tag = 0x0609
	TagBroadcastServiceGroup      Tag = 0x060A
	TagBillingIdentification      Tag = 0x060B
	TagSourceNetworkID            Tag = 0x060D
	TagDestNetworkID              Tag = 0x060E
	TagSourceNodeID               Tag = 0x060F
	TagDestNodeID                 Tag = 0x0610
	TagDestAddrNPResolution       Tag = 0x0611
	TagDestAddrNPInformation      Tag = 0x0612
	TagDestAddrNPCountry          Tag = 0x0613
)

// TLVSet holds optional TLV parameters for an SMPP PDU.
type TLVSet map[Tag][]byte

// Has reports whether the TLVSet contains the given tag.
func (t TLVSet) Has(tag Tag) bool {
	_, ok := t[tag]
	return ok
}

// GetUint8 returns the uint8 value for tag. The second return value is false
// if the tag is absent or the value is not exactly 1 byte.
func (t TLVSet) GetUint8(tag Tag) (uint8, bool) {
	v, ok := t[tag]
	if !ok || len(v) != 1 {
		return 0, false
	}
	return v[0], true
}

// GetUint16 returns the uint16 value for tag in big-endian byte order.
// The second return value is false if the tag is absent or the value is not
// exactly 2 bytes.
func (t TLVSet) GetUint16(tag Tag) (uint16, bool) {
	v, ok := t[tag]
	if !ok || len(v) != 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(v), true
}

// GetUint32 returns the uint32 value for tag in big-endian byte order.
// The second return value is false if the tag is absent or the value is not
// exactly 4 bytes.
func (t TLVSet) GetUint32(tag Tag) (uint32, bool) {
	v, ok := t[tag]
	if !ok || len(v) != 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(v), true
}

// GetString returns the string value for tag, stripping a trailing null byte
// if present. The second return value is false if the tag is absent.
func (t TLVSet) GetString(tag Tag) (string, bool) {
	v, ok := t[tag]
	if !ok {
		return "", false
	}
	if len(v) > 0 && v[len(v)-1] == 0x00 {
		return string(v[:len(v)-1]), true
	}
	return string(v), true
}

// GetBytes returns a copy of the raw byte value for tag. The second return
// value is false if the tag is absent.
func (t TLVSet) GetBytes(tag Tag) ([]byte, bool) {
	v, ok := t[tag]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// SetUint8 stores a uint8 value for tag.
func (t TLVSet) SetUint8(tag Tag, v uint8) {
	t[tag] = []byte{v}
}

// SetUint16 stores a uint16 value for tag in big-endian byte order.
func (t TLVSet) SetUint16(tag Tag, v uint16) {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	t[tag] = b
}

// SetUint32 stores a uint32 value for tag in big-endian byte order.
func (t TLVSet) SetUint32(tag Tag, v uint32) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	t[tag] = b
}

// SetString stores a string value for tag (without adding a null terminator).
func (t TLVSet) SetString(tag Tag, v string) {
	t[tag] = []byte(v)
}

// SetBytes stores a raw byte value for tag. The value is copied.
func (t TLVSet) SetBytes(tag Tag, v []byte) {
	b := make([]byte, len(v))
	copy(b, v)
	t[tag] = b
}

// tlvHeaderLen is the size of a TLV header (tag + length) in bytes.
const tlvHeaderLen = 4

// Encode serialises all TLVs to wire format. Tags are written in ascending
// order for deterministic output. Each TLV is encoded as:
//
//	tag:    2 bytes (big-endian)
//	length: 2 bytes (big-endian)
//	value:  length bytes
func (t TLVSet) Encode() []byte {
	if len(t) == 0 {
		return nil
	}

	// Collect and sort tags for deterministic output.
	tags := make([]Tag, 0, len(t))
	for tag := range t {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })

	// Calculate total size.
	size := 0
	for _, tag := range tags {
		size += tlvHeaderLen + len(t[tag])
	}

	buf := make([]byte, size)
	offset := 0
	for _, tag := range tags {
		v := t[tag]
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(tag))
		binary.BigEndian.PutUint16(buf[offset+2:offset+4], uint16(len(v)))
		copy(buf[offset+4:], v)
		offset += tlvHeaderLen + len(v)
	}
	return buf
}

// DecodeTLVs parses wire-format TLV bytes into a TLVSet. An empty or nil
// input returns an empty (non-nil) TLVSet. Returns an error if the data is
// truncated (not enough bytes for a TLV header or declared value length).
// Zero-length value TLVs are valid per the SMPP specification.
func DecodeTLVs(data []byte) (TLVSet, error) {
	t := make(TLVSet)
	offset := 0
	for offset < len(data) {
		if offset+tlvHeaderLen > len(data) {
			return nil, fmt.Errorf("truncated TLV header at offset %d: need %d bytes, have %d",
				offset, tlvHeaderLen, len(data)-offset)
		}
		tag := Tag(binary.BigEndian.Uint16(data[offset : offset+2]))
		length := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		offset += tlvHeaderLen

		if offset+length > len(data) {
			return nil, fmt.Errorf("truncated TLV value for tag 0x%04X at offset %d: need %d bytes, have %d",
				uint16(tag), offset, length, len(data)-offset)
		}
		v := make([]byte, length)
		copy(v, data[offset:offset+length])
		t[tag] = v
		offset += length
	}
	return t, nil
}
