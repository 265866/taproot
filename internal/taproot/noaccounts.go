package taproot

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// unknownProvider starts upstream's error message for a model no credential
// serves (sdk/api/handlers/handlers_routing.go). Every proxy endpoint returns
// it as a 400 JSON body with the message at error.message.
const unknownProvider = "unknown provider for model "

// noAccounts makes upstream's "unknown provider for model X" say what is
// wrong when the pool has no account at all, which is how every new server
// starts. Upstream builds that message deep in its handlers, so a middleware
// holds back 400 responses to POSTs and rewrites the message on the way out.
// Every other response passes straight through.
type noAccounts struct {
	auths atomic.Pointer[coreauth.Manager]
}

func (n *noAccounts) attach(m *coreauth.Manager) {
	if m != nil {
		n.auths.Store(m)
	}
}

func (n *noAccounts) poolEmpty() bool {
	m := n.auths.Load()
	return m != nil && len(m.List()) == 0
}

func (n *noAccounts) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		w := &heldErrorWriter{ResponseWriter: c.Writer}
		c.Writer = w
		defer func() { c.Writer = w.ResponseWriter }()
		c.Next()
		if !w.held {
			return
		}
		body := w.body.Bytes()
		if rewritten, ok := rewriteUnknownProvider(body); ok && n.poolEmpty() {
			body = rewritten
			w.Header().Del("Content-Length")
		}
		w.ResponseWriter.WriteHeader(w.status)
		if _, err := w.ResponseWriter.Write(body); err != nil {
			log.WithError(err).Debug("taproot: write held error response")
		}
	}
}

// rewriteUnknownProvider replaces upstream's message in a JSON error body.
func rewriteUnknownProvider(body []byte) ([]byte, bool) {
	msg := ojson.GetBytes(body, "error.message")
	if msg.Type != ojson.String || !strings.HasPrefix(msg.Str, unknownProvider) {
		return nil, false
	}
	model := strings.TrimPrefix(msg.Str, unknownProvider)
	out, err := ojson.SetBytes(body, "error.message", "no accounts configured: sign in to an account on the taproot dashboard's Accounts page, then retry model "+model)
	if err != nil {
		return nil, false
	}
	return out, true
}

// heldErrorWriter holds back a 400 response until the handler returns, and
// passes every other status through untouched, streams included.
type heldErrorWriter struct {
	gin.ResponseWriter
	held   bool
	status int
	body   bytes.Buffer
}

func (w *heldErrorWriter) WriteHeader(code int) {
	switch {
	case code == http.StatusBadRequest && !w.ResponseWriter.Written():
		w.held, w.status = true, code
	case w.held && w.body.Len() == 0:
		// The handler changed its mind before writing a byte.
		w.held = false
		w.ResponseWriter.WriteHeader(code)
	case !w.held:
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *heldErrorWriter) WriteHeaderNow() {
	if !w.held {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *heldErrorWriter) Write(p []byte) (int, error) {
	if w.held {
		return w.body.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *heldErrorWriter) WriteString(s string) (int, error) {
	if w.held {
		return w.body.WriteString(s)
	}
	return w.ResponseWriter.WriteString(s)
}

func (w *heldErrorWriter) Status() int {
	if w.held {
		return w.status
	}
	return w.ResponseWriter.Status()
}

func (w *heldErrorWriter) Size() int {
	if w.held {
		return w.body.Len()
	}
	return w.ResponseWriter.Size()
}

func (w *heldErrorWriter) Written() bool {
	return (w.held && w.body.Len() > 0) || w.ResponseWriter.Written()
}

func (w *heldErrorWriter) Flush() {
	if !w.held {
		w.ResponseWriter.Flush()
	}
}
