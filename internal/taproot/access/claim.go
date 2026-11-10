package access

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// ClaimPath is where a tailnet device posts the setup code. The settings
// module serves it under taproot's API prefix; while the server is unclaimed
// the guard lets tailnet devices without Admin reach it, and nothing else.
const ClaimPath = "/taproot/api/v1/claim"

const (
	// claimAlphabet leaves out 0, O, 1, I, and L, which read alike.
	claimAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	claimCodeLen  = 8
	// claimMaxFailures wrong codes from one device within claimWindow lock
	// the claim for that device until the oldest one is claimWindow old, and
	// claimMaxTotalFailures from all devices lock it for everyone. One device
	// alone cannot lock out the others, and guessing stays infeasible.
	claimMaxFailures      = 5
	claimMaxTotalFailures = 30
	claimWindow           = time.Minute
)

var (
	// ErrClaimClosed means the server has an admin device or no setup code.
	ErrClaimClosed = errors.New("this server is already claimed")
	// ErrClaimWrongCode means the code does not match.
	ErrClaimWrongCode = errors.New("wrong setup code")
)

// ClaimRateLimitedError means too many wrong codes were tried within a minute.
type ClaimRateLimitedError struct {
	RetryAfter time.Duration
}

func (e *ClaimRateLimitedError) Error() string {
	return fmt.Sprintf("too many wrong setup codes; try again in %s", e.RetryAfter.Round(time.Second))
}

// setupClaim is the one-time setup code and the wrong attempts against it.
type setupClaim struct {
	mu sync.Mutex
	// code is "" while no claim waits.
	code string
	// failures are each device's recent wrong codes, by stable node ID.
	failures map[string][]time.Time
	// announce receives each new code; it is nil until StartClaim.
	announce func(code string)
}

// newClaimCode returns claimCodeLen characters of claimAlphabet from crypto/rand.
func newClaimCode() (string, error) {
	max := big.NewInt(int64(len(claimAlphabet)))
	b := make([]byte, claimCodeLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate setup code: %w", err)
		}
		b[i] = claimAlphabet[n.Int64()]
	}
	return string(b), nil
}

// FormatClaimCode writes a code as XXXX-XXXX.
func FormatClaimCode(code string) string {
	if len(code) != claimCodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// normalizeClaimCode accepts the code the way people type it: any case, with
// or without the dash and spaces.
func normalizeClaimCode(raw string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(raw)))
}

// StartClaim arms the setup code when the guard is on. From then on, while
// no device can use Admin, a code waits, and announce receives each new one:
// at once, and again whenever a change leaves no device that can use Admin
// after a code was used up. Setup calls it once and logs the code.
func (g *Guard) StartClaim(ctx context.Context, announce func(code string)) error {
	if !g.enabled {
		return nil
	}
	g.claim.mu.Lock()
	g.claim.announce = announce
	g.claim.mu.Unlock()
	if self := g.self(context.WithoutCancel(ctx)); self != nil {
		if d, ok := g.grants.Load().byID[self.StableID]; ok && d.Admin {
			log.Warnf("taproot access: access.devices gives Admin to %s (%s), which is this server's own node and never gets Admin; remove admin: true from that entry", cmp.Or(d.Name, self.DNSName), d.ID)
		}
	}
	return g.reviewClaim()
}

// reviewClaim makes a setup code and announces it when the claim is armed,
// no code waits, and no device can use Admin.
func (g *Guard) reviewClaim() error {
	g.claim.mu.Lock()
	if g.claim.announce == nil || g.claim.code != "" || g.hasAdmin() {
		g.claim.mu.Unlock()
		return nil
	}
	code, err := newClaimCode()
	if err != nil {
		g.claim.mu.Unlock()
		return err
	}
	g.claim.code, g.claim.failures = code, nil
	announce := g.claim.announce
	g.claim.mu.Unlock()
	announce(code)
	return nil
}

// restartClaim runs reviewClaim after a change that may have removed the last
// device that can use Admin.
func (g *Guard) restartClaim() {
	if err := g.reviewClaim(); err != nil {
		log.WithError(err).Error("taproot access: no device can use Admin, and taproot could not make a setup code")
	}
}

// Claimable reports whether a setup code is waiting and no device can use Admin.
func (g *Guard) Claimable() bool {
	g.claim.mu.Lock()
	defer g.claim.mu.Unlock()
	return g.claim.code != "" && !g.hasAdmin()
}

// CheckClaimCode compares code, sent by the device nodeID, with the setup
// code. Wrong codes count against claimMaxFailures per claimWindow for that
// device and claimMaxTotalFailures for all of them; while a limit is reached,
// every code is refused unchecked with a *ClaimRateLimitedError, so guessing
// gains nothing. now is the caller's clock.
func (g *Guard) CheckClaimCode(nodeID, code string, now time.Time) error {
	g.claim.mu.Lock()
	defer g.claim.mu.Unlock()
	if g.claim.code == "" || g.hasAdmin() {
		return ErrClaimClosed
	}
	var all []time.Time
	for id, times := range g.claim.failures {
		recent := slices.DeleteFunc(times, func(at time.Time) bool { return now.Sub(at) >= claimWindow })
		if len(recent) == 0 {
			delete(g.claim.failures, id)
			continue
		}
		g.claim.failures[id] = recent
		all = append(all, recent...)
	}
	if own := g.claim.failures[nodeID]; len(own) >= claimMaxFailures {
		return &ClaimRateLimitedError{RetryAfter: own[0].Add(claimWindow).Sub(now)}
	}
	if len(all) >= claimMaxTotalFailures {
		return &ClaimRateLimitedError{RetryAfter: slices.MinFunc(all, time.Time.Compare).Add(claimWindow).Sub(now)}
	}
	if subtle.ConstantTimeCompare([]byte(normalizeClaimCode(code)), []byte(g.claim.code)) != 1 {
		if g.claim.failures == nil {
			g.claim.failures = map[string][]time.Time{}
		}
		g.claim.failures[nodeID] = append(g.claim.failures[nodeID], now)
		return ErrClaimWrongCode
	}
	return nil
}

// EndClaim uses up the setup code. The settings module calls it once a
// device has Admin, through a claim or an edit.
func (g *Guard) EndClaim() {
	g.claim.mu.Lock()
	defer g.claim.mu.Unlock()
	g.claim.code, g.claim.failures = "", nil
}

// ServerDNSName returns the server's MagicDNS name, reading tailscaled's
// status if it has not been read yet, or "" when the guard is off or
// tailscaled does not answer.
func (g *Guard) ServerDNSName(ctx context.Context) string {
	if !g.enabled {
		return ""
	}
	// Other callers may wait on this fetch, so it outlives a caller that hangs up.
	if s := g.self(context.WithoutCancel(ctx)); s != nil {
		return s.DNSName
	}
	return ""
}

// hasAdmin reports whether a device the guard can give Admin has it. Once the
// server's own node is known, its grant does not count, since admit never
// gives it Admin.
func (g *Guard) hasAdmin() bool {
	self, _ := g.cachedSelf()
	return slices.ContainsFunc(g.grants.Load().Devices, func(d DeviceGrant) bool {
		return d.Admin && (self == nil || d.ID != self.StableID)
	})
}
