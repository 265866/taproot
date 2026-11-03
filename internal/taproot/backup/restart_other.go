//go:build !unix

package backup

// selfRestart is nil where a process cannot ask itself to shut down
// gracefully; restores then tell the operator to restart by hand.
var selfRestart func() error
