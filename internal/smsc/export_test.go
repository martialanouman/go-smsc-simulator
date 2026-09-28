package smsc

import "time"

// SetSessionTimeouts shortens the write and idle timeouts of every virtual SMSC so the
// black-box tests can provoke write_timeout and idle_timeout in milliseconds. Call it
// between New and Serve: sessions read these fields, and starting Serve orders the write
// before any session exists.
func SetSessionTimeouts(e *Engine, write, idle time.Duration) {
	for _, v := range e.smscs {
		v.writeTimeout = write
		v.idleTimeout = idle
	}
}
