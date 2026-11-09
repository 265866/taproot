package settings

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type claimRequest struct {
	Code string `json:"code"`
}

type claimResponse struct {
	Claimed bool               `json:"claimed"`
	Device  access.DeviceGrant `json:"device"`
}

// handleClaim serves POST /claim: a tailnet device that sends the setup code
// gets Proxy and Admin, through the same live apply, taproot.yaml write, and
// rollback as PUT /settings.
func (s *Settings) handleClaim(c *gin.Context) {
	var body claimRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		writeError(c, badRequest("invalid_body", "claim body: %v", err))
		return
	}
	caller, _ := s.identityFrom(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, err := s.claim(body.Code, caller)
	if err != nil {
		var limited *access.ClaimRateLimitedError
		if errors.As(err, &limited) {
			retryAfter := int(math.Ceil(limited.RetryAfter.Seconds()))
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": "rate_limited", "message": limited.Error(), "retryAfter": retryAfter}})
			return
		}
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, claimResponse{Claimed: true, Device: grant})
}

// claim checks the caller and the code, then grants the caller's device Proxy
// and Admin. Callers that are not a tailnet device are refused before the code
// is read, so they never count against the attempt limit.
func (s *Settings) claim(code string, caller access.Identity) (access.DeviceGrant, error) {
	if !s.deps.Access.Claimable() {
		return access.DeviceGrant{}, &apiError{
			status: http.StatusConflict, code: "claimed",
			message: "this server is already claimed: on a device with Admin, turn on Admin for this one on the Access page",
		}
	}
	if caller.Via != access.ViaTailnet || caller.NodeID == "" {
		return access.DeviceGrant{}, &apiError{
			status: http.StatusForbidden, code: "claim_tailnet_only",
			message: "only a tailnet device can claim this server: open the dashboard from the device that should become its first admin",
		}
	}
	switch server := s.deps.Access.ServerNodeID(); {
	case server == "":
		return access.DeviceGrant{}, &apiError{
			status: http.StatusServiceUnavailable, code: "server_node_unknown",
			message: "taproot has not read its own tailnet node from tailscaled yet; try again in a few seconds",
		}
	case caller.NodeID == server:
		return access.DeviceGrant{}, &apiError{
			status: http.StatusForbidden, code: "claim_tailnet_only",
			message: "the taproot server's own node never has Admin: open the dashboard from the device that should become its first admin",
		}
	}
	name, _, _ := strings.Cut(strings.TrimSuffix(caller.Node, "."), ".")
	if err := s.deps.Access.CheckClaimCode(code, s.now()); err != nil {
		if errors.Is(err, access.ErrClaimWrongCode) {
			log.Warnf("taproot access: wrong setup code from %s (%s)", name, caller.NodeID)
			return access.DeviceGrant{}, &apiError{status: http.StatusForbidden, code: "wrong_code", message: "that is not the setup code: look for \"setup code\" in taproot's log"}
		}
		if errors.Is(err, access.ErrClaimClosed) {
			return access.DeviceGrant{}, &apiError{status: http.StatusConflict, code: "claimed", message: "this server is already claimed"}
		}
		return access.DeviceGrant{}, err
	}

	grant := access.DeviceGrant{ID: caller.NodeID, Name: name, Proxy: true, Admin: true}
	devices := slices.DeleteFunc(s.deps.Access.Grants().Devices, func(d access.DeviceGrant) bool { return d.ID == grant.ID })
	devices = append(devices, grant)
	if err := s.apply(update{Access: &accessUpdate{Devices: &devices}}, caller); err != nil {
		return access.DeviceGrant{}, fmt.Errorf("grant %s Admin: %w", grant.ID, err)
	}
	log.Infof("taproot access: %s (%s) claimed this server with the setup code and now has Proxy and Admin; the setup code is used up", name, grant.ID)
	return grant, nil
}
