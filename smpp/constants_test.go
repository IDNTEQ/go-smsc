package smpp

import (
	"fmt"
	"testing"
)

// =============================================================================
// Command ID uniqueness
// =============================================================================

// TestCommandID_NoDuplicates verifies that every command ID constant has a
// unique value. This covers both the constants defined in pdu.go and those
// added in constants.go.
func TestCommandID_NoDuplicates(t *testing.T) {
	commands := []struct {
		name  string
		value uint32
	}{
		// From pdu.go
		{"CmdBindTransceiver", CmdBindTransceiver},
		{"CmdBindTransceiverResp", CmdBindTransceiverResp},
		{"CmdSubmitSM", CmdSubmitSM},
		{"CmdSubmitSMResp", CmdSubmitSMResp},
		{"CmdDeliverSM", CmdDeliverSM},
		{"CmdDeliverSMResp", CmdDeliverSMResp},
		{"CmdEnquireLink", CmdEnquireLink},
		{"CmdEnquireLinkResp", CmdEnquireLinkResp},
		{"CmdUnbind", CmdUnbind},
		{"CmdUnbindResp", CmdUnbindResp},
		{"CmdGenericNack", CmdGenericNack},

		// SMPP 3.4 from constants.go
		{"CmdBindReceiver", CmdBindReceiver},
		{"CmdBindReceiverResp", CmdBindReceiverResp},
		{"CmdBindTransmitter", CmdBindTransmitter},
		{"CmdBindTransmitterResp", CmdBindTransmitterResp},
		{"CmdQuerySM", CmdQuerySM},
		{"CmdQuerySMResp", CmdQuerySMResp},
		{"CmdReplaceSM", CmdReplaceSM},
		{"CmdReplaceSMResp", CmdReplaceSMResp},
		{"CmdCancelSM", CmdCancelSM},
		{"CmdCancelSMResp", CmdCancelSMResp},
		{"CmdSubmitMulti", CmdSubmitMulti},
		{"CmdSubmitMultiResp", CmdSubmitMultiResp},
		{"CmdOutbind", CmdOutbind},
		{"CmdAlertNotification", CmdAlertNotification},
		{"CmdDataSM", CmdDataSM},
		{"CmdDataSMResp", CmdDataSMResp},

		// SMPP 5.0 from constants.go
		{"CmdBroadcastSM", CmdBroadcastSM},
		{"CmdBroadcastSMResp", CmdBroadcastSMResp},
		{"CmdQueryBroadcastSM", CmdQueryBroadcastSM},
		{"CmdQueryBroadcastSMResp", CmdQueryBroadcastSMResp},
		{"CmdCancelBroadcastSM", CmdCancelBroadcastSM},
		{"CmdCancelBroadcastSMResp", CmdCancelBroadcastSMResp},
	}

	seen := make(map[uint32]string, len(commands))
	for _, cmd := range commands {
		if prev, ok := seen[cmd.value]; ok {
			t.Errorf("duplicate command ID value 0x%08X: %s and %s", cmd.value, prev, cmd.name)
		}
		seen[cmd.value] = cmd.name
	}
}

// =============================================================================
// Status code uniqueness
// =============================================================================

// TestStatusCode_NoDuplicates verifies that every status code constant has a
// unique value. This covers both the constants defined in pdu.go and those
// added in constants.go.
func TestStatusCode_NoDuplicates(t *testing.T) {
	statuses := []struct {
		name  string
		value uint32
	}{
		// From pdu.go
		{"StatusOK", StatusOK},
		{"StatusInvMsgLen", StatusInvMsgLen},
		{"StatusInvCmdLen", StatusInvCmdLen},
		{"StatusInvCmdID", StatusInvCmdID},
		{"StatusInvBnd", StatusInvBnd},
		{"StatusSysErr", StatusSysErr},
		{"StatusThrottled", StatusThrottled},
		{"StatusMsgQFull", StatusMsgQFull},
		{"StatusSubmitFail", StatusSubmitFail},

		// SMPP 3.4 from constants.go
		{"StatusAlyBnd", StatusAlyBnd},
		{"StatusInvPrtFlg", StatusInvPrtFlg},
		{"StatusCantDel", StatusCantDel},
		{"StatusInvSrcAdr", StatusInvSrcAdr},
		{"StatusInvDstAdr", StatusInvDstAdr},
		{"StatusInvMsgID", StatusInvMsgID},
		{"StatusBindFail", StatusBindFail},
		{"StatusInvPaswd", StatusInvPaswd},
		{"StatusInvSysID", StatusInvSysID},
		{"StatusCancelFail", StatusCancelFail},
		{"StatusReplaceFail", StatusReplaceFail},
		{"StatusInvDftMsgID", StatusInvDftMsgID},
		{"StatusRInvExpiry", StatusRInvExpiry},
		{"StatusInvNumDe", StatusInvNumDe},
		{"StatusInvDLName", StatusInvDLName},
		{"StatusInvDstFlag", StatusInvDstFlag},
		{"StatusInvSubRep", StatusInvSubRep},
		{"StatusInvEsmClass", StatusInvEsmClass},
		{"StatusCntSubDL", StatusCntSubDL},
		{"StatusInvSched", StatusInvSched},
		{"StatusInvOptParStream", StatusInvOptParStream},
		{"StatusOptParNotAllwd", StatusOptParNotAllwd},
		{"StatusInvParLen", StatusInvParLen},
		{"StatusMissingOptParam", StatusMissingOptParam},
		{"StatusInvOptParVal", StatusInvOptParVal},
		{"StatusDeliveryFailure", StatusDeliveryFailure},
		{"StatusUnknownErr", StatusUnknownErr},

		// SMPP 5.0 from constants.go
		{"StatusSvcTypUnauth", StatusSvcTypUnauth},
		{"StatusProhibited", StatusProhibited},
		{"StatusSvcTypUnavail", StatusSvcTypUnavail},
		{"StatusSvcTypDenied", StatusSvcTypDenied},
		{"StatusInvDCS", StatusInvDCS},
		{"StatusInvSrcAddrSubunit", StatusInvSrcAddrSubunit},
		{"StatusInvDstAddrSubunit", StatusInvDstAddrSubunit},
		{"StatusInvBcastFreqInt", StatusInvBcastFreqInt},
		{"StatusInvBcastAliasName", StatusInvBcastAliasName},
		{"StatusInvBcastAreaFmt", StatusInvBcastAreaFmt},
		{"StatusInvNBcastAreas", StatusInvNBcastAreas},
		{"StatusInvBcastCntType", StatusInvBcastCntType},
		{"StatusInvBcastMsgClass", StatusInvBcastMsgClass},
		{"StatusBcastFail", StatusBcastFail},
		{"StatusBcastQueryFail", StatusBcastQueryFail},
		{"StatusBcastCancelFail", StatusBcastCancelFail},
		{"StatusInvBcastRep", StatusInvBcastRep},
		{"StatusInvBcastSrvGrp", StatusInvBcastSrvGrp},
		{"StatusInvBcastChanInd", StatusInvBcastChanInd},
	}

	seen := make(map[uint32]string, len(statuses))
	for _, s := range statuses {
		if prev, ok := seen[s.value]; ok {
			t.Errorf("duplicate status code value 0x%08X: %s and %s", s.value, prev, s.name)
		}
		seen[s.value] = s.name
	}
}

// =============================================================================
// CommandName
// =============================================================================

func TestCommandName_Known(t *testing.T) {
	tests := []struct {
		id   uint32
		want string
	}{
		{CmdBindReceiver, "bind_receiver"},
		{CmdBindReceiverResp, "bind_receiver_resp"},
		{CmdBindTransmitter, "bind_transmitter"},
		{CmdBindTransmitterResp, "bind_transmitter_resp"},
		{CmdSubmitSM, "submit_sm"},
		{CmdSubmitSMResp, "submit_sm_resp"},
		{CmdDeliverSM, "deliver_sm"},
		{CmdDeliverSMResp, "deliver_sm_resp"},
		{CmdUnbind, "unbind"},
		{CmdUnbindResp, "unbind_resp"},
		{CmdBindTransceiver, "bind_transceiver"},
		{CmdBindTransceiverResp, "bind_transceiver_resp"},
		{CmdEnquireLink, "enquire_link"},
		{CmdEnquireLinkResp, "enquire_link_resp"},
		{CmdGenericNack, "generic_nack"},
		{CmdQuerySM, "query_sm"},
		{CmdQuerySMResp, "query_sm_resp"},
		{CmdReplaceSM, "replace_sm"},
		{CmdReplaceSMResp, "replace_sm_resp"},
		{CmdCancelSM, "cancel_sm"},
		{CmdCancelSMResp, "cancel_sm_resp"},
		{CmdSubmitMulti, "submit_multi"},
		{CmdSubmitMultiResp, "submit_multi_resp"},
		{CmdOutbind, "outbind"},
		{CmdAlertNotification, "alert_notification"},
		{CmdDataSM, "data_sm"},
		{CmdDataSMResp, "data_sm_resp"},
		{CmdBroadcastSM, "broadcast_sm"},
		{CmdBroadcastSMResp, "broadcast_sm_resp"},
		{CmdQueryBroadcastSM, "query_broadcast_sm"},
		{CmdQueryBroadcastSMResp, "query_broadcast_sm_resp"},
		{CmdCancelBroadcastSM, "cancel_broadcast_sm"},
		{CmdCancelBroadcastSMResp, "cancel_broadcast_sm_resp"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := CommandName(tt.id)
			if got != tt.want {
				t.Errorf("CommandName(0x%08X) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestCommandName_Unknown(t *testing.T) {
	tests := []struct {
		id   uint32
		want string
	}{
		{0xDEADBEEF, "unknown_0xDEADBEEF"},
		{0x00000000, "unknown_0x00000000"},
		{0xFFFFFFFF, "unknown_0xFFFFFFFF"},
		{0x00000999, "unknown_0x00000999"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := CommandName(tt.id)
			if got != tt.want {
				t.Errorf("CommandName(0x%08X) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// =============================================================================
// StatusName
// =============================================================================

func TestStatusName_Known(t *testing.T) {
	tests := []struct {
		status uint32
		want   string
	}{
		{StatusOK, "ESME_ROK"},
		{StatusInvMsgLen, "ESME_RINVMSGLEN"},
		{StatusInvCmdID, "ESME_RINVCMDID"},
		{StatusSysErr, "ESME_RSYSERR"},
		{StatusThrottled, "ESME_RTHROTTLED"},
		{StatusAlyBnd, "ESME_RALYBND"},
		{StatusInvSrcAdr, "ESME_RINVSRCADR"},
		{StatusInvDstAdr, "ESME_RINVDSTADR"},
		{StatusBindFail, "ESME_RBINDFAIL"},
		{StatusDeliveryFailure, "ESME_RDELIVERYFAILURE"},
		{StatusUnknownErr, "ESME_RUNKNOWNERR"},
		{StatusSvcTypUnauth, "ESME_RSVCTYP_UNAUTH"},
		{StatusProhibited, "ESME_RPROHIBITED"},
		{StatusInvDCS, "ESME_RINVDCS"},
		{StatusBcastFail, "ESME_RBCASTFAIL"},
		{StatusInvBcastChanInd, "ESME_RINVBCASTCHANIND"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := StatusName(tt.status)
			if got != tt.want {
				t.Errorf("StatusName(0x%08X) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestStatusName_Unknown(t *testing.T) {
	tests := []struct {
		status uint32
		want   string
	}{
		{0xDEADBEEF, "unknown_0xDEADBEEF"},
		{0x00000999, "unknown_0x00000999"},
		{0xFFFFFFFF, "unknown_0xFFFFFFFF"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := StatusName(tt.status)
			if got != tt.want {
				t.Errorf("StatusName(0x%08X) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Message state constants
// =============================================================================

func TestMsgState_Values(t *testing.T) {
	tests := []struct {
		name  string
		state byte
		want  byte
	}{
		{"MsgStateScheduled", MsgStateScheduled, 0},
		{"MsgStateEnRoute", MsgStateEnRoute, 1},
		{"MsgStateDelivered", MsgStateDelivered, 2},
		{"MsgStateExpired", MsgStateExpired, 3},
		{"MsgStateDeleted", MsgStateDeleted, 4},
		{"MsgStateUndeliverable", MsgStateUndeliverable, 5},
		{"MsgStateAccepted", MsgStateAccepted, 6},
		{"MsgStateUnknown", MsgStateUnknown, 7},
		{"MsgStateRejected", MsgStateRejected, 8},
		{"MsgStateSkipped", MsgStateSkipped, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.state != tt.want {
				t.Errorf("%s = %d, want %d", tt.name, tt.state, tt.want)
			}
		})
	}
}

// =============================================================================
// CommandName / StatusName coverage — every map entry is reachable
// =============================================================================

func TestCommandName_AllMapped(t *testing.T) {
	// Verify every entry in commandNames is returned correctly.
	for id, want := range commandNames {
		got := CommandName(id)
		if got != want {
			t.Errorf("CommandName(0x%08X) = %q, want %q", id, got, want)
		}
	}
}

func TestStatusName_AllMapped(t *testing.T) {
	// Verify every entry in statusNames is returned correctly.
	for code, want := range statusNames {
		got := StatusName(code)
		if got != want {
			t.Errorf("StatusName(0x%08X) = %q, want %q", code, got, want)
		}
	}
}

// =============================================================================
// Verify commandNames and statusNames map sizes match constant counts
// =============================================================================

func TestCommandName_MapCompleteness(t *testing.T) {
	// All command IDs that should be in the map.
	allCmds := []uint32{
		CmdBindReceiver, CmdBindReceiverResp,
		CmdBindTransmitter, CmdBindTransmitterResp,
		CmdQuerySM, CmdQuerySMResp,
		CmdSubmitSM, CmdSubmitSMResp,
		CmdDeliverSM, CmdDeliverSMResp,
		CmdUnbind, CmdUnbindResp,
		CmdReplaceSM, CmdReplaceSMResp,
		CmdCancelSM, CmdCancelSMResp,
		CmdBindTransceiver, CmdBindTransceiverResp,
		CmdOutbind,
		CmdEnquireLink, CmdEnquireLinkResp,
		CmdSubmitMulti, CmdSubmitMultiResp,
		CmdAlertNotification,
		CmdDataSM, CmdDataSMResp,
		CmdGenericNack,
		CmdBroadcastSM, CmdBroadcastSMResp,
		CmdQueryBroadcastSM, CmdQueryBroadcastSMResp,
		CmdCancelBroadcastSM, CmdCancelBroadcastSMResp,
	}

	for _, id := range allCmds {
		name := CommandName(id)
		if name == fmt.Sprintf("unknown_0x%08X", id) {
			t.Errorf("command ID 0x%08X missing from commandNames map", id)
		}
	}
}
