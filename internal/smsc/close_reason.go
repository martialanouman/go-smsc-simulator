package smsc

import (
	"errors"
	"net"
)

// closeReason names why a session ended. The set is closed — it is the reason label of
// smsc_session_closed_total — and every path that ends a session maps to exactly one value
// (spec §5.2, "Fermetures de session").
type closeReason string

const (
	reasonClientUnbind        closeReason = "client_unbind"         // the ESME sent unbind
	reasonClientEOF           closeReason = "client_eof"            // the ESME closed the TCP connection
	reasonReadError           closeReason = "read_error"            // the read failed otherwise (reset, TLS…)
	reasonWriteError          closeReason = "write_error"           // a write failed (reset, broken pipe)
	reasonWriteTimeout        closeReason = "write_timeout"         // a write stayed blocked past writeTimeout
	reasonIdleTimeout         closeReason = "idle_timeout"          // no PDU for idleTimeout, nothing pending
	reasonPartialFrameTimeout closeReason = "partial_frame_timeout" // the read deadline expired mid-frame
	reasonProtocolError       closeReason = "protocol_error"        // unresynchronisable command_length
	reasonAuthFailed          closeReason = "auth_failed"           // bind with wrong credentials
	reasonBindRejected        closeReason = "bind_rejected"         // dead-carrier reject_bind
	reasonFaultInjection      closeReason = "fault_injection"       // the scenario drew a disconnect outcome
	reasonScheduledDisconnect closeReason = "scheduled_disconnect"  // a scheduled_disconnects entry fired
	reasonShutdown            closeReason = "shutdown"              // the process is stopping
	reasonPanic               closeReason = "panic"                 // the session goroutine panicked
)

// closeReasons lists the whole closed set, for tests and documentation.
var closeReasons = []closeReason{
	reasonClientUnbind, reasonClientEOF, reasonReadError, reasonWriteError, reasonWriteTimeout,
	reasonIdleTimeout, reasonPartialFrameTimeout, reasonProtocolError, reasonAuthFailed,
	reasonBindRejected, reasonFaultInjection, reasonScheduledDisconnect, reasonShutdown, reasonPanic,
}

// initiator reports which side ended the session: "client" when the peer hung up, reset
// the link or unbound, "server" for every close the simulator decided or caused.
func (r closeReason) initiator() string {
	switch r {
	case reasonClientUnbind, reasonClientEOF, reasonReadError, reasonWriteError:
		return "client"
	default:
		return "server"
	}
}

// writeFailure classifies a failed write: its own deadline means the server gave up on a
// client that stopped reading (write_timeout); anything else is a broken link (write_error).
func writeFailure(err error) closeReason {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return reasonWriteTimeout
	}
	return reasonWriteError
}
