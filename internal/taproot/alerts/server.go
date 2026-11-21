package alerts

import (
	"fmt"
	"time"
)

// ServerReport is the status module's judgment of the server as a whole, from
// its latest evaluation, so the Dashboard and the alerts read one judgment.
type ServerReport struct {
	// RequestsJudged is set when the verdict's window held enough requests
	// to judge a failure rate. RequestsFailing is set while the verdict is
	// down because at least half of them failed.
	RequestsJudged  bool
	RequestsFailing bool
	Requests        int64
	Failed          int64
	// Backup is the Needs-you backup item, or nil when there is none.
	Backup *BackupProblem
}

// BackupProblem is an overdue or failed backup, or a backup list taproot
// could not read, as Needs you words it.
type BackupProblem struct {
	Title  string
	Detail string
}

// Keys of the conditions that concern the server rather than an account.
const (
	keyRequests = "requests"
	keyBackup   = "backup"
)

// requestsDelay is how long most requests must keep failing before the
// alert posts. The verdict judges 15 minutes of requests, so a short burst
// of failures can hold it for a while on its own.
const requestsDelay = 5 * time.Minute

// Report records the status module's latest judgment.
func (s *Service) Report(r ServerReport) {
	s.serverMu.Lock()
	s.server, s.serverSeen = r, true
	s.serverMu.Unlock()
}

// carryServerTracks applies what the latest report says about open server
// alerts it cannot judge alone. Until the first report after a start, both
// keys stay as they are, because the alert state is restored before the
// status module has evaluated. A requests window under the verdict's 20
// requests never opens an alert, but one that mostly fails keeps an open
// alert open, and one that mostly succeeds ends it; an empty one changes
// nothing. So clients that give up on a failing server do not read as a
// recovery, and light traffic that succeeds does.
func (d *Detector) carryServerTracks(conds map[string]Condition, r ServerReport, seen bool) {
	if !seen {
		d.hold(conds, keyRequests)
		d.hold(conds, keyBackup)
		return
	}
	t := d.tracks[keyRequests]
	if r.RequestsJudged || t == nil {
		return
	}
	switch {
	case r.Requests == 0:
		d.hold(conds, keyRequests)
	case r.Failed*2 >= r.Requests && t.Notified:
		conds[keyRequests] = requestsCondition(r)
	case r.Failed*2 >= r.Requests:
		d.hold(conds, keyRequests)
	}
}

// hold keeps the key's track as it is while it is active.
func (d *Detector) hold(conds map[string]Condition, key string) {
	if t := d.tracks[key]; t != nil && t.Active {
		if _, ok := conds[key]; !ok {
			conds[key] = Condition{Hold: true}
		}
	}
}

func serverConditions(r ServerReport, out map[string]Condition) {
	if r.RequestsFailing {
		out[keyRequests] = requestsCondition(r)
	}
	if b := r.Backup; b != nil {
		out[keyBackup] = Condition{
			Alert: Message{
				Key: keyBackup, Severity: SeverityWarning,
				Title:       safeLabel(b.Title),
				Description: safeText(b.Detail, 400),
			},
			Recovery: &Message{
				Key: keyBackup, Severity: SeverityRecovered,
				Title:       "Backups are back on track",
				Description: "The dashboard no longer lists a backup problem.",
			},
		}
	}
}

func requestsCondition(r ServerReport) Condition {
	return Condition{
		Alert: Message{
			Key: keyRequests, Severity: SeverityCritical,
			Title:       "Taproot is failing most requests",
			Description: "Most client requests in the last 15 minutes failed. Request Logs on the dashboard has the errors.",
			Fields: []Field{
				{Name: "Failed", Value: fmt.Sprintf("%d of %d (%d%%)", r.Failed, r.Requests, r.Failed*100/max(r.Requests, 1))},
			},
		},
		Recovery: &Message{
			Key: keyRequests, Severity: SeverityRecovered,
			Title:       "Taproot serves requests again",
			Description: "Fewer than half of the client requests in the last 15 minutes failed.",
		},
		Delay: requestsDelay,
	}
}
