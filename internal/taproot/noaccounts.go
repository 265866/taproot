package taproot

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/265866/taproot/internal/httpwire"
	"github.com/265866/taproot/internal/registry"
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
// wrong with the pool: it has no account at all, which is how every new
// server starts, every account is disabled, or no enabled account serves the
// model. Upstream builds that message deep in its handlers, so a middleware
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

// Codes of a request no account could serve, which the client request record
// keeps as its error code.
const (
	codeNoAccounts       = "no_accounts"
	codeAccountsDisabled = "accounts_disabled"
	codeUnknownProvider  = "unknown_provider"
)

// noAccountKey holds, in the gin context, the noAccount of a request
// upstream refused because no account serves its model.
const noAccountKey = "taproot.noAccount"

// noAccount is why no account served a request, and the model it named.
type noAccount struct{ code, model string }

// message explains why no account serves model, with its code, or returns
// false before the manager is attached.
func (n *noAccounts) message(model string) (code, text string, ok bool) {
	m := n.auths.Load()
	if m == nil {
		return codeUnknownProvider, "", false
	}
	auths := m.List()
	if len(auths) == 0 {
		return codeNoAccounts, "no accounts configured: sign in to an account on the taproot dashboard's Accounts page, then retry model " + model, true
	}
	if !slices.ContainsFunc(auths, func(a *coreauth.Auth) bool { return !a.Disabled }) {
		return codeAccountsDisabled, "every account is disabled: enable one on the taproot dashboard's Accounts page, then retry model " + model, true
	}
	if provider := modelProvider(model); provider != "" {
		if slices.ContainsFunc(auths, func(a *coreauth.Auth) bool { return !a.Disabled && strings.EqualFold(a.Provider, provider) }) {
			return codeUnknownProvider, "no enabled " + provider + " account serves model " + model + ": check the model name; the pool's " + provider + " accounts do not list it", true
		}
		return codeUnknownProvider, "no enabled " + provider + " account serves model " + model + ": add or enable one on the taproot dashboard's Accounts page", true
	}
	return codeUnknownProvider, "no enabled account serves model " + model + ": check the model name, or add an account that serves it on the taproot dashboard's Accounts page", true
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
		if model, ok := unknownProviderModel(body); ok {
			code, text, known := n.message(model)
			c.Set(noAccountKey, noAccount{code: code, model: model})
			if rewritten, ok := rewriteMessage(body, text); known && ok {
				body = rewritten
				w.Header().Del("Content-Length")
			}
		}
		w.ResponseWriter.WriteHeader(w.status)
		if _, err := w.ResponseWriter.Write(body); err != nil {
			log.WithError(err).Debug("taproot: write held error response")
		}
	}
}

// modelProvider names the provider whose accounts serve model, for the two
// providers the dashboard signs in to, or returns "". Clients often send an
// alias, such as claude-sonnet-4-5, that the static catalog lists only with
// its date, so the name's prefix counts too.
func modelProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "claude-") || registry.LookupStaticModelInfoByChannel(model, "claude") != nil:
		return "Claude"
	case strings.HasPrefix(model, "gpt-") || registry.LookupStaticModelInfoByChannel(model, "codex") != nil:
		return "Codex"
	}
	return ""
}

// unknownProviderModel returns the model of upstream's "unknown provider"
// error in a JSON error body.
func unknownProviderModel(body []byte) (string, bool) {
	msg := ojson.GetBytes(body, "error.message")
	if msg.Type != ojson.String || !strings.HasPrefix(msg.Str, unknownProvider) {
		return "", false
	}
	return strings.TrimPrefix(msg.Str, unknownProvider), true
}

// rewriteMessage replaces error.message in a JSON error body.
func rewriteMessage(body []byte, text string) ([]byte, bool) {
	out, err := ojson.SetBytes(body, "error.message", text)
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

// FlushError and Unwrap keep upstream's error-aware flush and
// http.ResponseController reaching the transport, as its own wrappers do:
// streaming handlers cancel the upstream request on a flush error.
func (w *heldErrorWriter) FlushError() error {
	if w.held {
		return nil
	}
	return httpwire.FlushResponse(w.ResponseWriter)
}

func (w *heldErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
