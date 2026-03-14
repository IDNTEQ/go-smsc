package smpp

import "bytes"

// ---------------------------------------------------------------------------
// broadcast_sm (SMPP 5.0 -- section 4.6.1)
// ---------------------------------------------------------------------------

// EncodeBroadcastSM creates the mandatory body for a broadcast_sm PDU.
// Mandatory fields: service_type(C) + source_addr_ton(1) + source_addr_npi(1) +
//
//	source_addr(C) + message_id(C) + priority_flag(1) +
//	schedule_delivery_time(C) + validity_period(C) +
//	replace_if_present(1) + data_coding(1) + sm_default_msg_id(1)
//
// Note: broadcast_sm REQUIRES certain TLVs (broadcast_area_identifier,
// broadcast_content_type, broadcast_rep_num, broadcast_frequency_interval) --
// these should be set on PDU.TLVs by the caller.
func EncodeBroadcastSM(serviceType string, sourceTON, sourceNPI byte,
	sourceAddr, messageID string, priorityFlag byte,
	scheduleDeliveryTime, validityPeriod string,
	replaceIfPresent, dataCoding, smDefaultMsgID byte) []byte {

	var buf bytes.Buffer
	writeCString(&buf, serviceType)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	writeCString(&buf, messageID)
	buf.WriteByte(priorityFlag)
	writeCString(&buf, scheduleDeliveryTime)
	writeCString(&buf, validityPeriod)
	buf.WriteByte(replaceIfPresent)
	buf.WriteByte(dataCoding)
	buf.WriteByte(smDefaultMsgID)
	return buf.Bytes()
}

// ParseBroadcastSMResp parses a broadcast_sm_resp body.
// Returns the message_id. TLVs (broadcast_error_status, etc.) should be
// extracted separately via ExtractTLVs.
func ParseBroadcastSMResp(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	msgID, _ := readCString(body, 0)
	return msgID
}

// ---------------------------------------------------------------------------
// query_broadcast_sm (SMPP 5.0 -- section 4.6.2)
// ---------------------------------------------------------------------------

// EncodeQueryBroadcastSM creates the mandatory body for a query_broadcast_sm PDU.
// Mandatory fields: message_id(C) + source_addr_ton(1) + source_addr_npi(1) +
//
//	source_addr(C)
func EncodeQueryBroadcastSM(messageID string, sourceTON, sourceNPI byte, sourceAddr string) []byte {
	var buf bytes.Buffer
	writeCString(&buf, messageID)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	return buf.Bytes()
}

// ParseQueryBroadcastSMResp parses a query_broadcast_sm_resp body.
// Returns message_id and message_state. TLVs should be extracted separately.
func ParseQueryBroadcastSMResp(body []byte) (messageID string, messageState byte) {
	if len(body) == 0 {
		return "", 0
	}
	offset := 0
	messageID, offset = readCString(body, offset)
	if offset < len(body) {
		messageState = body[offset]
	}
	return messageID, messageState
}

// ---------------------------------------------------------------------------
// cancel_broadcast_sm (SMPP 5.0 -- section 4.6.3)
// ---------------------------------------------------------------------------

// EncodeCancelBroadcastSM creates the mandatory body for a cancel_broadcast_sm PDU.
// Mandatory fields: service_type(C) + message_id(C) + source_addr_ton(1) +
//
//	source_addr_npi(1) + source_addr(C)
func EncodeCancelBroadcastSM(serviceType, messageID string, sourceTON, sourceNPI byte, sourceAddr string) []byte {
	var buf bytes.Buffer
	writeCString(&buf, serviceType)
	writeCString(&buf, messageID)
	buf.WriteByte(sourceTON)
	buf.WriteByte(sourceNPI)
	writeCString(&buf, sourceAddr)
	return buf.Bytes()
}

// cancel_broadcast_sm_resp has empty body (just status in header)
