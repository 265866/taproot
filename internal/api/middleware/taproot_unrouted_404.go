package middleware

import (
	"github.com/265866/taproot/internal/logging"
	"github.com/gin-gonic/gin"
)

// taprootKeepUnrouted404Key marks a request whose logger stores 404s for
// routes the server does not serve, so isUnroutedNotFound treats it as routed.
const taprootKeepUnrouted404Key = "taproot.keepUnrouted404"

// taprootMarkUnrouted404Kept marks c and reports true when logger keeps
// unrouted 404s. Taproot's logger does when request-logs.keep-unrouted-404 is
// on; upstream's loggers never do, so upstream's behavior is unchanged.
func taprootMarkUnrouted404Kept(c *gin.Context, logger logging.RequestLogger) bool {
	keeper, ok := logger.(interface{ KeepsUnrouted404() bool })
	if !ok || !keeper.KeepsUnrouted404() {
		return false
	}
	c.Set(taprootKeepUnrouted404Key, true)
	return true
}
