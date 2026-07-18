package scenario

import (
	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// statusFor translates a config error code (from scenario.params) to the wire
// command_status the codec emits. It lives here, not in smpp, so the codec stays free
// of any config import (a deliberate boundary noted in smpp/commands.go): scenario
// already owns config vocabulary and imports smpp for CommandStatus.
//
// Config validation guarantees the code is one of the known set before boot, so the
// default is defensive only.
func statusFor(c config.SMPPErrorCode) smpp.CommandStatus {
	switch c {
	case config.ErrorCodeROK:
		return smpp.StatusROK
	case config.ErrorCodeRThrottled:
		return smpp.StatusThrottled
	case config.ErrorCodeRSubmitFail:
		return smpp.StatusSubmitFail
	case config.ErrorCodeRInvDstAdr:
		return smpp.StatusInvDstAdr
	case config.ErrorCodeRSysErr:
		return smpp.StatusSysErr
	case config.ErrorCodeRMsgQFul:
		return smpp.StatusMsgQFul
	case config.ErrorCodeRInvSrcAdr:
		return smpp.StatusInvSrcAdr
	default:
		return smpp.StatusSysErr
	}
}
