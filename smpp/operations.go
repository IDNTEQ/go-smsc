package smpp

import (
	"bytes"
	"encoding/binary"
)

// ---------------------------------------------------------------------------
// query_sm (§4.8)
// ---------------------------------------------------------------------------

// EncodeQuerySM builds the body of a query_sm PDU.
func EncodeQuerySM(messageID, sourceAddr string, sourceTON, sourceNPI byte) []byte {
	var buf bytes.Buffer
	writeCString(&buf, messageID)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	return buf.Bytes()
}

// ParseQuerySMResp extracts fields from a query_sm_resp body.
// Returns the message_id, final_date, message_state, and error_code.
func ParseQuerySMResp(body []byte) (messageID, finalDate string, messageState, errorCode byte) {
	if len(body) == 0 {
		return "", "", 0, 0
	}
	offset := 0
	messageID, offset = readCString(body, offset)
	finalDate, offset = readCString(body, offset)
	if offset < len(body) {
		messageState = body[offset]
		offset++
	}
	if offset < len(body) {
		errorCode = body[offset]
	}
	return messageID, finalDate, messageState, errorCode
}

// ---------------------------------------------------------------------------
// cancel_sm (§4.9)
// ---------------------------------------------------------------------------

// EncodeCancelSM builds the body of a cancel_sm PDU.
func EncodeCancelSM(serviceType, messageID, sourceAddr string, sourceTON, sourceNPI byte, destAddr string, destTON, destNPI byte) []byte {
	var buf bytes.Buffer
	writeCString(&buf, serviceType)
	writeCString(&buf, messageID)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	buf.WriteByte(destTON)
	buf.WriteByte(destNPI)
	writeCString(&buf, destAddr)
	return buf.Bytes()
}

// cancel_sm_resp has empty body (just status in header)

// ---------------------------------------------------------------------------
// replace_sm (§4.10)
// ---------------------------------------------------------------------------

// EncodeReplaceSM builds the body of a replace_sm PDU.
func EncodeReplaceSM(messageID, sourceAddr string, sourceTON, sourceNPI byte,
	scheduleDeliveryTime, validityPeriod string,
	registeredDelivery, dataCoding, smLength byte, shortMessage []byte) []byte {

	var buf bytes.Buffer
	writeCString(&buf, messageID)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	writeCString(&buf, scheduleDeliveryTime)
	writeCString(&buf, validityPeriod)
	buf.WriteByte(registeredDelivery)
	buf.WriteByte(0x00) // sm_default_msg_id
	buf.WriteByte(smLength)
	buf.Write(shortMessage)
	return buf.Bytes()
}

// replace_sm_resp has empty body

// ---------------------------------------------------------------------------
// data_sm (§4.7)
// ---------------------------------------------------------------------------

// EncodeDataSM builds the body of a data_sm PDU.
func EncodeDataSM(serviceType string, sourceTON, sourceNPI byte, sourceAddr string,
	destTON, destNPI byte, destAddr string,
	esmClass, registeredDelivery, dataCoding byte) []byte {

	var buf bytes.Buffer
	writeCString(&buf, serviceType)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	buf.WriteByte(destTON)
	buf.WriteByte(destNPI)
	writeCString(&buf, destAddr)
	buf.WriteByte(esmClass)
	buf.WriteByte(registeredDelivery)
	buf.WriteByte(dataCoding)
	return buf.Bytes()
}

// ParseDataSMResp extracts the message_id from a data_sm_resp body.
func ParseDataSMResp(body []byte) (messageID string) {
	if len(body) == 0 {
		return ""
	}
	messageID, _ = readCString(body, 0)
	return messageID
}

// ---------------------------------------------------------------------------
// submit_multi (§4.5)
// ---------------------------------------------------------------------------

// DestAddress represents a destination in a submit_multi PDU.
type DestAddress struct {
	Flag    byte   // 0x01 = SME address, 0x02 = distribution list
	TON     byte
	NPI     byte
	Address string
}

// UnsuccessDest represents an unsuccessful delivery attempt in submit_multi_resp.
type UnsuccessDest struct {
	TON       byte
	NPI       byte
	Address   string
	ErrorCode uint32
}

// EncodeSubmitMulti builds the body of a submit_multi PDU.
func EncodeSubmitMulti(serviceType string, sourceTON, sourceNPI byte, sourceAddr string,
	dests []DestAddress,
	esmClass, protocolID, priorityFlag byte,
	scheduleDeliveryTime, validityPeriod string,
	registeredDelivery, dataCoding byte,
	shortMessage []byte) []byte {

	var buf bytes.Buffer
	writeCString(&buf, serviceType)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)

	// number_of_dests
	buf.WriteByte(byte(len(dests)))
	for _, d := range dests {
		buf.WriteByte(d.Flag)
		if d.Flag == 0x02 {
			// Distribution list name — just a C-string.
			writeCString(&buf, d.Address)
		} else {
			// SME address: TON + NPI + address C-string.
			buf.WriteByte(d.TON)
			buf.WriteByte(d.NPI)
			writeCString(&buf, d.Address)
		}
	}

	buf.WriteByte(esmClass)
	buf.WriteByte(protocolID)
	buf.WriteByte(priorityFlag)
	writeCString(&buf, scheduleDeliveryTime)
	writeCString(&buf, validityPeriod)
	buf.WriteByte(registeredDelivery)
	buf.WriteByte(0x00) // replace_if_present_flag
	buf.WriteByte(dataCoding)
	buf.WriteByte(0x00) // sm_default_msg_id

	// sm_length + short_message
	buf.WriteByte(byte(len(shortMessage)))
	buf.Write(shortMessage)

	return buf.Bytes()
}

// ParseSubmitMultiResp extracts the message_id and unsuccess list from a
// submit_multi_resp body.
func ParseSubmitMultiResp(body []byte) (messageID string, unsuccess []UnsuccessDest) {
	if len(body) == 0 {
		return "", nil
	}
	offset := 0
	messageID, offset = readCString(body, offset)

	// no_unsuccess (1 byte)
	if offset >= len(body) {
		return messageID, nil
	}
	numUnsuccess := int(body[offset])
	offset++

	for i := 0; i < numUnsuccess && offset < len(body); i++ {
		var u UnsuccessDest
		// dest_addr_ton
		if offset >= len(body) {
			break
		}
		u.TON = body[offset]
		offset++

		// dest_addr_npi
		if offset >= len(body) {
			break
		}
		u.NPI = body[offset]
		offset++

		// destination_addr
		u.Address, offset = readCString(body, offset)

		// error_status_code (4 bytes)
		if offset+4 > len(body) {
			break
		}
		u.ErrorCode = binary.BigEndian.Uint32(body[offset : offset+4])
		offset += 4

		unsuccess = append(unsuccess, u)
	}

	return messageID, unsuccess
}

// ---------------------------------------------------------------------------
// alert_notification (§4.12) — MC→ESME only, no response
// ---------------------------------------------------------------------------

// ParseAlertNotification extracts fields from an alert_notification PDU body.
func ParseAlertNotification(body []byte) (sourceTON, sourceNPI byte, sourceAddr string, esmeTON, esmeNPI byte, esmeAddr string) {
	if len(body) < 2 {
		return 0, 0, "", 0, 0, ""
	}
	offset := 0

	sourceTON = body[offset]
	offset++
	sourceNPI = body[offset]
	offset++
	sourceAddr, offset = readCString(body, offset)

	if offset >= len(body) {
		return sourceTON, sourceNPI, sourceAddr, 0, 0, ""
	}
	esmeTON = body[offset]
	offset++

	if offset >= len(body) {
		return sourceTON, sourceNPI, sourceAddr, esmeTON, 0, ""
	}
	esmeNPI = body[offset]
	offset++

	esmeAddr, _ = readCString(body, offset)

	return sourceTON, sourceNPI, sourceAddr, esmeTON, esmeNPI, esmeAddr
}
