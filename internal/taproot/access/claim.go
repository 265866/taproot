package access

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// ClaimPath is where a tailnet device posts the setup code. The settings
// module serves it under taproot's API prefix; while the server is unclaimed
// the guard lets tailnet devices without Admin reach it, and nothing else.
const ClaimPath = "/taproot/api/v1/claim"

const (
	// claimAlphabet leaves out 0, O, 1, I, and L, which read alike.
	claimAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	claimCodeLen  = 8
	// claimMaxFailures wrong codes within claimWindow lock the claim for
	// everyone until the oldest one is claimWindow old.
	claimMaxFailures = 5
	claimWindow      = time.Minute
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
	// code is "" before StartClaim and after the claim ends.
	code     string
	failures []time.Time
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

// StartClaim makes a new setup code when the guard is on and no device has
// Admin, and returns it; otherwise it returns "". Setup calls it once per
// start and logs the code, so removing every admin: true entry and restarting
// brings the claim back.
func (g *Guard) StartClaim() (string, error) {
	if !g.enabled || g.grants.Load().hasAdmin() {
		return "", nil
	}
	code, err := newClaimCode()
	if err != nil {
		return "", err
	}
	g.claim.mu.Lock()
	defer g.claim.mu.Unlock()
	g.claim.code, g.claim.failures = code, nil
	return code, nil
}

// Claimable reports whether a setup code is waiting and no device has Admin.
func (g *Guard) Claimable() bool {
	g.claim.mu.Lock()
	defer g.claim.mu.Unlock()
	return g.claim.code != "" && !g.grants.Load().hasAdmin()
}

// CheckClaimCode compares code with the setup code. Wrong codes count
// against claimMaxFailures per claimWindow for the whole server; while that
// limit is reached, every code is refused unchecked with a
// *ClaimRateLimitedError, so guessing gains nothing. now is the caller's
// clock.
func (g *Guard) CheckClaimCode(code string, now time.Time) error {
	g.claim.mu.Lock()
	defer g.claim.mu.Unlock()
	if g.claim.code == "" || g.grants.Load().hasAdmin() {
		return ErrClaimClosed
	}
	recent := g.claim.failures[:0]
	for _, at := range g.claim.failures {
		if now.Sub(at) < claimWindow {
			recent = append(recent, at)
		}
	}
	g.claim.failures = recent
	if len(recent) >= claimMaxFailures {
		return &ClaimRateLimitedError{RetryAfter: recent[0].Add(claimWindow).Sub(now)}
	}
	if subtle.ConstantTimeCompare([]byte(normalizeClaimCode(code)), []byte(g.claim.code)) != 1 {
		g.claim.failures = append(g.claim.failures, now)
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
	if s := g.self(ctx); s != nil {
		return s.DNSName
	}
	return ""
}

func (s *grantSet) hasAdmin() bool {
	for _, d := range s.Devices {
		if d.Admin {
			return true
		}
	}
	return false
}
