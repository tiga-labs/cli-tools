// Package chromeattach attaches to the user's running Chrome over the DevTools protocol,
// opens one tab of its own in the default profile, runs JavaScript in it, and closes it.
// It never touches the user's other tabs and never closes Chrome.
package chromeattach

import "errors"

// Setup errors. Their text is shown to the user as-is, so each one says what to do next.
var (
	ErrDebuggingOff      = errors.New("Chrome remote debugging is off. In Chrome 144 or newer, open chrome://inspect/#remote-debugging, turn it on, then run this again.")
	ErrChromeUnreachable = errors.New("could not reach Chrome. Make sure Chrome is running with remote debugging on (chrome://inspect/#remote-debugging). If you just restarted Chrome, wait a few seconds and run this again.")
	ErrDenied            = errors.New(`Chrome's "Allow remote debugging?" prompt was declined. Run this again and click Allow.`)
	ErrApprovalTimeout   = errors.New(`no answer to Chrome's "Allow remote debugging?" prompt in time. Run this again and click Allow.`)
	ErrConnectionClosed  = errors.New("Chrome closed the connection")
)
