package alerts

import (
	"fmt"
	"time"
)

// TailscaledOutage is tailscaled's LocalAPI going down or answering again,
// as the access guard's monitor reports it.
type TailscaledOutage struct {
	Socket string
	// Down is true for an outage, false for the recovery after one.
	Down  bool
	Since time.Time
	At    time.Time
	Err   string
	// StaleSocket is true when the socket is missing or nobody listens on it.
	StaleSocket bool
}

// Tailscaled queues the message for an outage or its recovery. The monitor
// calls it once for each, and both share one key, so a recovery replaces an
// outage message that is still waiting. While alerts are off it queues nothing.
func (s *Service) Tailscaled(o TailscaledOutage) {
	if !s.Active() {
		return
	}
	s.sender.Enqueue(tailscaledMessage(o))
}

func tailscaledMessage(o TailscaledOutage) Message {
	socket := safeLabel(o.Socket)
	if !o.Down {
		return Message{
			Key: "tailscaled", Severity: SeverityRecovered, At: o.At,
			Title:       "Taproot reaches tailscaled again",
			Description: fmt.Sprintf("tailscaled answers on %s again after %s, so taproot identifies tailnet devices again.", socket, o.At.Sub(o.Since).Round(time.Second)),
		}
	}
	fix := "Check that tailscaled runs and is logged in on the host (tailscale status)."
	if o.StaleSocket {
		fix = "If tailscaled restarted or updated, taproot likely still holds its old socket: restart taproot. To keep this from happening again, see \"Every tailnet device is refused after tailscaled restarts or updates\" in taproot's troubleshooting guide."
	}
	return Message{
		Key: "tailscaled", Severity: SeverityCritical, At: o.At,
		Title:       "Taproot can't reach tailscaled",
		Description: fmt.Sprintf("Calls to tailscaled on %s have failed for %s, so taproot refuses every tailnet device, admins included. %s", socket, o.At.Sub(o.Since).Round(time.Second), fix),
		Fields: []Field{
			{Name: "Error", Value: safeLabel(o.Err)},
			{Name: "Failing since", Value: discordTime(o.Since)},
		},
	}
}
