package alerts

import (
	"fmt"
	"time"
)

// AccessRequest is a device that just asked for access to taproot.
type AccessRequest struct {
	NodeID string
	// Name is the name the device gave itself or its Tailscale name, or "".
	Name   string
	Login  string
	Client string
	At     time.Time
}

// AccessRequested queues one message for a request that opened or reopened.
// The access module calls it once per request, never per refusal. While
// alerts are off it queues nothing.
func (s *Service) AccessRequested(r AccessRequest) {
	if !s.Active() {
		return
	}
	s.sender.Enqueue(accessRequestMessage(r))
}

func accessRequestMessage(r AccessRequest) Message {
	name := "A device"
	if r.Name != "" {
		name = safeLabel(r.Name)
	}
	login := safeLabel(r.Login)
	desc := fmt.Sprintf("**%s** asked for access to taproot. Allow or deny it under Requests on the Access page.", name)
	if login != "" {
		desc = fmt.Sprintf("**%s** (%s) asked for access to taproot. Allow or deny it under Requests on the Access page.", name, login)
	}
	fields := []Field{}
	if login != "" {
		fields = append(fields, Field{Name: "Login", Value: login})
	}
	if r.Client != "" {
		fields = append(fields, Field{Name: "Client", Value: safeLabel(r.Client)})
	}
	fields = append(fields, Field{Name: "Device ID", Value: safeLabel(r.NodeID)})
	return Message{
		Key: "access:" + r.NodeID, Severity: SeverityInfo, At: r.At,
		Title:       "Access requested",
		Description: desc,
		Fields:      fields,
	}
}
