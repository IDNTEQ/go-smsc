package smpp

// MandatoryBodyLen walks the mandatory fields for known PDU types to determine
// where TLVs begin within the PDU body. It returns the byte offset into body
// that marks the end of mandatory fields (and the start of any optional TLVs),
// or -1 if the command ID is unknown or the body is too short/malformed to
// determine the boundary.
func MandatoryBodyLen(commandID uint32, body []byte) int {
	switch commandID {
	case CmdSubmitSM, CmdDeliverSM:
		return mandatoryLenSubmitDeliverSM(body)

	case CmdSubmitSMResp, CmdDeliverSMResp:
		return mandatoryLenCStringOnly(body)

	case CmdBindTransceiver, CmdBindTransmitter, CmdBindReceiver:
		return mandatoryLenBind(body)

	case CmdBindTransceiverResp, CmdBindTransmitterResp, CmdBindReceiverResp:
		return mandatoryLenCStringOnly(body)

	case CmdQuerySM:
		return mandatoryLenQuerySM(body)

	case CmdQuerySMResp:
		return mandatoryLenQuerySMResp(body)

	case CmdCancelSM:
		return mandatoryLenCancelSM(body)

	case CmdReplaceSM:
		return mandatoryLenReplaceSM(body)

	case CmdDataSM:
		return mandatoryLenDataSM(body)

	case CmdDataSMResp:
		return mandatoryLenCStringOnly(body)

	case CmdAlertNotification:
		return mandatoryLenAlertNotification(body)

	case CmdCancelSMResp, CmdReplaceSMResp:
		return 0

	case CmdEnquireLink, CmdEnquireLinkResp, CmdUnbind, CmdUnbindResp, CmdGenericNack:
		return 0

	case CmdSubmitMulti, CmdSubmitMultiResp:
		// Complex variable-length dest list; skip TLV extraction for now.
		return -1

	default:
		return -1
	}
}

// mandatoryLenSubmitDeliverSM calculates the mandatory field length for
// submit_sm and deliver_sm PDUs. The layout is:
//
//	service_type(C) + source_addr_ton(1) + source_addr_npi(1) + source_addr(C)
//	+ dest_addr_ton(1) + dest_addr_npi(1) + destination_addr(C)
//	+ esm_class(1) + protocol_id(1) + priority_flag(1)
//	+ schedule_delivery_time(C) + validity_period(C)
//	+ registered_delivery(1) + replace_if_present_flag(1) + data_coding(1)
//	+ sm_default_msg_id(1) + sm_length(1) + short_message(sm_length bytes)
func mandatoryLenSubmitDeliverSM(body []byte) int {
	offset := 0
	n := len(body)

	// service_type (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// source_addr_ton(1) + source_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// source_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// dest_addr_ton(1) + dest_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// destination_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// esm_class(1) + protocol_id(1) + priority_flag(1)
	if offset+3 > n {
		return -1
	}
	offset += 3

	// schedule_delivery_time (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// validity_period (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// registered_delivery(1) + replace_if_present_flag(1) + data_coding(1) + sm_default_msg_id(1)
	if offset+4 > n {
		return -1
	}
	offset += 4

	// sm_length(1)
	if offset >= n {
		return -1
	}
	smLen := int(body[offset])
	offset++

	// short_message(sm_length bytes)
	if offset+smLen > n {
		return -1
	}
	offset += smLen

	return offset
}

// mandatoryLenCStringOnly calculates the mandatory field length for PDU types
// whose body consists of a single C-string (e.g., submit_sm_resp,
// deliver_sm_resp, bind_*_resp).
func mandatoryLenCStringOnly(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	offset := skipCString(body, 0)
	if offset < 0 {
		return -1
	}
	return offset
}

// mandatoryLenBind calculates the mandatory field length for bind_transceiver,
// bind_transmitter, and bind_receiver PDUs. The layout is:
//
//	system_id(C) + password(C) + system_type(C)
//	+ interface_version(1) + addr_ton(1) + addr_npi(1) + address_range(C)
func mandatoryLenBind(body []byte) int {
	offset := 0
	n := len(body)

	// system_id (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// password (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// system_type (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// interface_version(1) + addr_ton(1) + addr_npi(1)
	if offset+3 > n {
		return -1
	}
	offset += 3

	// address_range (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	return offset
}

// skipCString scans for a null terminator starting at offset in body.
// Returns the offset just past the null terminator, or -1 if the null
// terminator is not found (truncated body).
func skipCString(body []byte, offset int) int {
	n := len(body)
	if offset >= n {
		return -1
	}
	for i := offset; i < n; i++ {
		if body[i] == 0x00 {
			return i + 1
		}
	}
	// No null terminator found — body is truncated.
	return -1
}

// mandatoryLenQuerySM calculates the mandatory field length for query_sm.
// Layout: message_id(C) + source_addr_ton(1) + source_addr_npi(1) + source_addr(C)
func mandatoryLenQuerySM(body []byte) int {
	offset := 0
	n := len(body)

	// message_id (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// source_addr_ton(1) + source_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// source_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	return offset
}

// mandatoryLenQuerySMResp calculates the mandatory field length for query_sm_resp.
// Layout: message_id(C) + final_date(C) + message_state(1) + error_code(1)
func mandatoryLenQuerySMResp(body []byte) int {
	offset := 0
	n := len(body)

	// message_id (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// final_date (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// message_state(1) + error_code(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	return offset
}

// mandatoryLenCancelSM calculates the mandatory field length for cancel_sm.
// Layout: service_type(C) + message_id(C) + source_addr_ton(1) + source_addr_npi(1)
//
//	+ source_addr(C) + dest_addr_ton(1) + dest_addr_npi(1) + destination_addr(C)
func mandatoryLenCancelSM(body []byte) int {
	offset := 0
	n := len(body)

	// service_type (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// message_id (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// source_addr_ton(1) + source_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// source_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// dest_addr_ton(1) + dest_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// destination_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	return offset
}

// mandatoryLenReplaceSM calculates the mandatory field length for replace_sm.
// Layout: message_id(C) + source_addr_ton(1) + source_addr_npi(1) + source_addr(C)
//
//	+ schedule_delivery_time(C) + validity_period(C)
//	+ registered_delivery(1) + sm_default_msg_id(1) + sm_length(1) + short_message(sm_length)
func mandatoryLenReplaceSM(body []byte) int {
	offset := 0
	n := len(body)

	// message_id (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// source_addr_ton(1) + source_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// source_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// schedule_delivery_time (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// validity_period (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// registered_delivery(1) + sm_default_msg_id(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// sm_length(1)
	if offset >= n {
		return -1
	}
	smLen := int(body[offset])
	offset++

	// short_message(sm_length)
	if offset+smLen > n {
		return -1
	}
	offset += smLen

	return offset
}

// mandatoryLenDataSM calculates the mandatory field length for data_sm.
// Layout: service_type(C) + source_addr_ton(1) + source_addr_npi(1) + source_addr(C)
//
//	+ dest_addr_ton(1) + dest_addr_npi(1) + destination_addr(C)
//	+ esm_class(1) + registered_delivery(1) + data_coding(1)
func mandatoryLenDataSM(body []byte) int {
	offset := 0
	n := len(body)

	// service_type (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// source_addr_ton(1) + source_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// source_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// dest_addr_ton(1) + dest_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// destination_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// esm_class(1) + registered_delivery(1) + data_coding(1)
	if offset+3 > n {
		return -1
	}
	offset += 3

	return offset
}

// mandatoryLenAlertNotification calculates the mandatory field length for
// alert_notification. Layout:
// source_addr_ton(1) + source_addr_npi(1) + source_addr(C)
// + esme_addr_ton(1) + esme_addr_npi(1) + esme_addr(C)
func mandatoryLenAlertNotification(body []byte) int {
	offset := 0
	n := len(body)

	// source_addr_ton(1) + source_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// source_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	// esme_addr_ton(1) + esme_addr_npi(1)
	if offset+2 > n {
		return -1
	}
	offset += 2

	// esme_addr (C-string)
	offset = skipCString(body, offset)
	if offset < 0 {
		return -1
	}

	return offset
}

// ExtractTLVs is a convenience function that determines the TLV boundary
// within a PDU's body and decodes any TLVs found after the mandatory fields.
// It does NOT modify the PDU — the caller can optionally assign the result
// to pdu.TLVs.
//
// Returns:
//   - (nil, nil) if the command ID is unknown (cannot determine TLV boundary)
//   - (TLVSet{}, nil) if the command is known but no TLVs are present
//   - (TLVSet with entries, nil) if TLVs were successfully decoded
//   - (nil, error) if the TLV bytes are malformed
func ExtractTLVs(pdu *PDU) (TLVSet, error) {
	offset := MandatoryBodyLen(pdu.CommandID, pdu.Body)
	if offset < 0 {
		// Unknown command — cannot determine TLV boundary.
		return nil, nil
	}
	if offset == len(pdu.Body) {
		// Known command, no TLV bytes present.
		return make(TLVSet), nil
	}
	return DecodeTLVs(pdu.Body[offset:])
}
