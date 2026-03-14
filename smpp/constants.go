package smpp

import "fmt"

// ---------------------------------------------------------------------------
// SMPP 3.4 command IDs (missing from pdu.go — those already defined there
// will be moved here in a later task).
// ---------------------------------------------------------------------------

const (
	CmdBindReceiver        uint32 = 0x00000001
	CmdBindReceiverResp    uint32 = 0x80000001
	CmdBindTransmitter     uint32 = 0x00000002
	CmdBindTransmitterResp uint32 = 0x80000002
	CmdQuerySM             uint32 = 0x00000003
	CmdQuerySMResp         uint32 = 0x80000003
	CmdReplaceSM           uint32 = 0x00000007
	CmdReplaceSMResp       uint32 = 0x80000007
	CmdCancelSM            uint32 = 0x00000008
	CmdCancelSMResp        uint32 = 0x80000008
	CmdSubmitMulti         uint32 = 0x00000021
	CmdSubmitMultiResp     uint32 = 0x80000021
	CmdOutbind             uint32 = 0x0000000B
	CmdAlertNotification   uint32 = 0x00000102
	CmdDataSM              uint32 = 0x00000103
	CmdDataSMResp          uint32 = 0x80000103
)

// ---------------------------------------------------------------------------
// SMPP 5.0 command IDs (broadcast operations).
// ---------------------------------------------------------------------------

const (
	CmdBroadcastSM           uint32 = 0x00000111
	CmdBroadcastSMResp       uint32 = 0x80000111
	CmdQueryBroadcastSM      uint32 = 0x00000112
	CmdQueryBroadcastSMResp  uint32 = 0x80000112
	CmdCancelBroadcastSM     uint32 = 0x00000113
	CmdCancelBroadcastSMResp uint32 = 0x80000113
)

// ---------------------------------------------------------------------------
// SMPP 3.4 status codes (missing from pdu.go).
// ---------------------------------------------------------------------------

const (
	StatusAlyBnd            uint32 = 0x00000005 // Already in bound state
	StatusInvPrtFlg         uint32 = 0x00000006 // Invalid priority flag
	StatusCantDel           uint32 = 0x00000007 // Cannot delete message
	StatusInvSrcAdr         uint32 = 0x0000000A // Invalid source address
	StatusInvDstAdr         uint32 = 0x0000000B // Invalid destination address
	StatusInvMsgID          uint32 = 0x0000000C // Invalid message ID
	StatusBindFail          uint32 = 0x0000000D // Bind failed
	StatusInvPaswd          uint32 = 0x0000000E // Invalid password
	StatusInvSysID          uint32 = 0x0000000F // Invalid system ID
	StatusCancelFail        uint32 = 0x00000011 // Cancel SM failed
	StatusReplaceFail       uint32 = 0x00000013 // Replace SM failed
	StatusInvDftMsgID       uint32 = 0x00000015 // Invalid default message ID
	StatusRInvExpiry        uint32 = 0x00000022 // Invalid expiry
	StatusInvNumDe          uint32 = 0x00000033 // Invalid number of destinations
	StatusInvDLName         uint32 = 0x00000034 // Invalid DL name
	StatusInvDstFlag        uint32 = 0x00000040 // Invalid destination flag
	StatusInvSubRep         uint32 = 0x00000042 // Invalid submit with replace request
	StatusInvEsmClass       uint32 = 0x00000043 // Invalid ESM class field data
	StatusCntSubDL          uint32 = 0x00000044 // Cannot submit to DL
	StatusInvSched          uint32 = 0x00000061 // Invalid scheduled delivery time
	StatusInvOptParStream   uint32 = 0x000000C0 // Invalid optional parameter stream
	StatusOptParNotAllwd    uint32 = 0x000000C1 // Optional parameter not allowed
	StatusInvParLen         uint32 = 0x000000C2 // Invalid parameter length
	StatusMissingOptParam   uint32 = 0x000000C3 // Missing expected optional parameter
	StatusInvOptParVal      uint32 = 0x000000C4 // Invalid optional parameter value
	StatusDeliveryFailure   uint32 = 0x000000FE // Delivery failure (data_sm)
	StatusUnknownErr        uint32 = 0x000000FF // Unknown error
)

// ---------------------------------------------------------------------------
// SMPP 5.0 status codes.
// ---------------------------------------------------------------------------

const (
	StatusSvcTypUnauth      uint32 = 0x00000100 // Not authorized to use service_type
	StatusProhibited        uint32 = 0x00000101 // ESME prohibited from operation
	StatusSvcTypUnavail     uint32 = 0x00000102 // service_type unavailable
	StatusSvcTypDenied      uint32 = 0x00000103 // service_type denied
	StatusInvDCS            uint32 = 0x00000104 // Invalid Data Coding Scheme
	StatusInvSrcAddrSubunit uint32 = 0x00000105 // Invalid source addr subunit
	StatusInvDstAddrSubunit uint32 = 0x00000106 // Invalid dest addr subunit
	StatusInvBcastFreqInt   uint32 = 0x00000107 // Invalid broadcast freq interval
	StatusInvBcastAliasName uint32 = 0x00000108 // Invalid broadcast alias name
	StatusInvBcastAreaFmt   uint32 = 0x00000109 // Invalid broadcast area format
	StatusInvNBcastAreas    uint32 = 0x0000010A // Invalid number of broadcast areas
	StatusInvBcastCntType   uint32 = 0x0000010B // Invalid broadcast content type
	StatusInvBcastMsgClass  uint32 = 0x0000010C // Invalid broadcast message class
	StatusBcastFail         uint32 = 0x0000010D // broadcast_sm failed
	StatusBcastQueryFail    uint32 = 0x0000010E // query_broadcast_sm failed
	StatusBcastCancelFail   uint32 = 0x0000010F // cancel_broadcast_sm failed
	StatusInvBcastRep       uint32 = 0x00000110 // Invalid number of repeated broadcasts
	StatusInvBcastSrvGrp    uint32 = 0x00000111 // Invalid broadcast service group
	StatusInvBcastChanInd   uint32 = 0x00000112 // Invalid broadcast channel indicator
)

// ---------------------------------------------------------------------------
// Registered delivery flags.
// ---------------------------------------------------------------------------

const (
	RegDeliveryNone    byte = 0x00 // No delivery receipt
	RegDeliveryBoth    byte = 0x01 // Success + failure delivery receipts
	RegDeliveryFailure byte = 0x02 // Failure only delivery receipt
	RegDeliverySuccess byte = 0x03 // Success only delivery receipt (SMPP 5.0)
)

// ---------------------------------------------------------------------------
// Message states (used in message_state TLV and query_sm_resp).
// ---------------------------------------------------------------------------

const (
	MsgStateScheduled     byte = 0 // SMPP 5.0
	MsgStateEnRoute       byte = 1
	MsgStateDelivered     byte = 2
	MsgStateExpired       byte = 3
	MsgStateDeleted       byte = 4
	MsgStateUndeliverable byte = 5
	MsgStateAccepted      byte = 6
	MsgStateUnknown       byte = 7
	MsgStateRejected      byte = 8
	MsgStateSkipped       byte = 9 // SMPP 5.0
)

// ---------------------------------------------------------------------------
// CommandName returns a human-readable name for an SMPP command ID.
// Unknown IDs return "unknown_0xXXXXXXXX".
// ---------------------------------------------------------------------------

var commandNames = map[uint32]string{
	// SMPP 3.4 commands
	CmdBindReceiver:        "bind_receiver",
	CmdBindReceiverResp:    "bind_receiver_resp",
	CmdBindTransmitter:     "bind_transmitter",
	CmdBindTransmitterResp: "bind_transmitter_resp",
	CmdQuerySM:             "query_sm",
	CmdQuerySMResp:         "query_sm_resp",
	CmdSubmitSM:            "submit_sm",
	CmdSubmitSMResp:        "submit_sm_resp",
	CmdDeliverSM:           "deliver_sm",
	CmdDeliverSMResp:       "deliver_sm_resp",
	CmdUnbind:              "unbind",
	CmdUnbindResp:          "unbind_resp",
	CmdReplaceSM:           "replace_sm",
	CmdReplaceSMResp:       "replace_sm_resp",
	CmdCancelSM:            "cancel_sm",
	CmdCancelSMResp:        "cancel_sm_resp",
	CmdBindTransceiver:     "bind_transceiver",
	CmdBindTransceiverResp: "bind_transceiver_resp",
	CmdOutbind:             "outbind",
	CmdEnquireLink:         "enquire_link",
	CmdEnquireLinkResp:     "enquire_link_resp",
	CmdSubmitMulti:         "submit_multi",
	CmdSubmitMultiResp:     "submit_multi_resp",
	CmdAlertNotification:   "alert_notification",
	CmdDataSM:              "data_sm",
	CmdDataSMResp:          "data_sm_resp",
	CmdGenericNack:         "generic_nack",

	// SMPP 5.0 broadcast commands
	CmdBroadcastSM:           "broadcast_sm",
	CmdBroadcastSMResp:       "broadcast_sm_resp",
	CmdQueryBroadcastSM:      "query_broadcast_sm",
	CmdQueryBroadcastSMResp:  "query_broadcast_sm_resp",
	CmdCancelBroadcastSM:     "cancel_broadcast_sm",
	CmdCancelBroadcastSMResp: "cancel_broadcast_sm_resp",
}

// CommandName returns a human-readable name for an SMPP command ID.
func CommandName(id uint32) string {
	if name, ok := commandNames[id]; ok {
		return name
	}
	return fmt.Sprintf("unknown_0x%08X", id)
}

// ---------------------------------------------------------------------------
// StatusName returns a human-readable name for an SMPP status code.
// Unknown codes return "unknown_0xXXXXXXXX".
// ---------------------------------------------------------------------------

var statusNames = map[uint32]string{
	// SMPP 3.4 status codes (from pdu.go)
	StatusOK:         "ESME_ROK",
	StatusInvMsgLen:  "ESME_RINVMSGLEN",
	StatusInvCmdLen:  "ESME_RINVCMDLEN",
	StatusInvCmdID:   "ESME_RINVCMDID",
	StatusInvBnd:     "ESME_RINVBNDSTS",
	StatusSysErr:     "ESME_RSYSERR",
	StatusThrottled:  "ESME_RTHROTTLED",
	StatusMsgQFull:   "ESME_RMSGQFUL",
	StatusSubmitFail: "ESME_RSUBMITFAIL",

	// SMPP 3.4 status codes (from constants.go)
	StatusAlyBnd:          "ESME_RALYBND",
	StatusInvPrtFlg:       "ESME_RINVPRTFLG",
	StatusCantDel:         "ESME_RCANTDEL",
	StatusInvSrcAdr:       "ESME_RINVSRCADR",
	StatusInvDstAdr:       "ESME_RINVDSTADR",
	StatusInvMsgID:        "ESME_RINVMSGID",
	StatusBindFail:        "ESME_RBINDFAIL",
	StatusInvPaswd:        "ESME_RINVPASWD",
	StatusInvSysID:        "ESME_RINVSYSID",
	StatusCancelFail:      "ESME_RCANCELFAIL",
	StatusReplaceFail:     "ESME_RREPLACEFAIL",
	StatusInvDftMsgID:     "ESME_RINVDFTMSGID",
	StatusRInvExpiry:      "ESME_RINVEXPIRY",
	StatusInvNumDe:        "ESME_RINVNUMDE",
	StatusInvDLName:       "ESME_RINVDLNAME",
	StatusInvDstFlag:      "ESME_RINVDSTFLAG",
	StatusInvSubRep:       "ESME_RINVSUBREP",
	StatusInvEsmClass:     "ESME_RINVESMCLASS",
	StatusCntSubDL:        "ESME_RCNTSUBDL",
	StatusInvSched:        "ESME_RINVSCHED",
	StatusInvOptParStream: "ESME_RINVOPTPARSTREAM",
	StatusOptParNotAllwd:  "ESME_ROPTPARNOTALLWD",
	StatusInvParLen:       "ESME_RINVPARLEN",
	StatusMissingOptParam: "ESME_RMISSINGOPTPARAM",
	StatusInvOptParVal:    "ESME_RINVOPTPARVAL",
	StatusDeliveryFailure: "ESME_RDELIVERYFAILURE",
	StatusUnknownErr:      "ESME_RUNKNOWNERR",

	// SMPP 5.0 status codes
	StatusSvcTypUnauth:      "ESME_RSVCTYP_UNAUTH",
	StatusProhibited:        "ESME_RPROHIBITED",
	StatusSvcTypUnavail:     "ESME_RSVCTYP_UNAVAIL",
	StatusSvcTypDenied:      "ESME_RSVCTYP_DENIED",
	StatusInvDCS:            "ESME_RINVDCS",
	StatusInvSrcAddrSubunit: "ESME_RINVSRCADDRSUBUNIT",
	StatusInvDstAddrSubunit: "ESME_RINVDSTADDRSUBUNIT",
	StatusInvBcastFreqInt:   "ESME_RINVBCASTFREQINT",
	StatusInvBcastAliasName: "ESME_RINVBCASTALIASNAME",
	StatusInvBcastAreaFmt:   "ESME_RINVBCASTAREAFMT",
	StatusInvNBcastAreas:    "ESME_RINVNBCASTAREAS",
	StatusInvBcastCntType:   "ESME_RINVBCASTCNTTYPE",
	StatusInvBcastMsgClass:  "ESME_RINVBCASTMSGCLASS",
	StatusBcastFail:         "ESME_RBCASTFAIL",
	StatusBcastQueryFail:    "ESME_RBCASTQUERYFAIL",
	StatusBcastCancelFail:   "ESME_RBCASTCANCELFAIL",
	StatusInvBcastRep:       "ESME_RINVBCASTREP",
	StatusInvBcastSrvGrp:    "ESME_RINVBCASTSRVGRP",
	StatusInvBcastChanInd:   "ESME_RINVBCASTCHANIND",
}

// StatusName returns a human-readable name for an SMPP status code.
func StatusName(status uint32) string {
	if name, ok := statusNames[status]; ok {
		return name
	}
	return fmt.Sprintf("unknown_0x%08X", status)
}
