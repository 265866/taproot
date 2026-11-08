// Package web serves the embedded taproot dashboard.
package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// dist is the dashboard build: index.html and its content-hashed chunks under
// assets/. scripts/taproot/build-web.sh replaces the committed placeholder.
//
//go:embed all:dist
var dist embed.FS

const assetsPrefix = "/assets/"

type file struct {
	body  []byte
	gz    []byte // nil when gzip would not shrink it
	ctype string
	etag  string
}

// load maps request paths ("/" and "/assets/<name>") to their bodies, each
// precompressed at gzip level 9 (about 110 ms for a 2.8 MB build). Chrome and
// Firefox offer only gzip and deflate to plain-http origins such as a tailnet
// name, so brotli and zstd would never be chosen there.
func load(fsys fs.FS, root string) map[string]*file {
	out := make(map[string]*file)
	err := fs.WalkDir(fsys, root, func(full string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(full, root+"/")
		key := "/" + name
		if name == "index.html" {
			key = "/"
		} else if !strings.HasPrefix(key, assetsPrefix) {
			return nil
		}
		body, err := fs.ReadFile(fsys, full)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		f := &file{body: body, etag: `W/"` + hex.EncodeToString(sum[:12]) + `"`}
		f.ctype = mime.TypeByExtension(path.Ext(name))
		if key == "/" {
			f.ctype = "text/html; charset=utf-8"
		} else if f.ctype == "" {
			f.ctype = "application/octet-stream"
		}
		if gz := gzipBytes(body); gz != nil && len(gz) < len(body) {
			f.gz = gz
		}
		out[key] = f
		return nil
	})
	if err != nil {
		log.WithError(err).Error("taproot web: read embedded dashboard")
	}
	return out
}

func gzipBytes(body []byte) []byte {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := zw.Write(body); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

// Middleware serves the embedded dashboard; see handler.
func Middleware() gin.HandlerFunc { return handler(dist, "dist") }

// handler serves the dashboard page for GET and HEAD on "/" and its chunks
// under /assets/, and passes every other request on. It is a middleware rather
// than a route because upstream registers its own GET "/" before router
// configurators run, and gin panics on a second registration of the same route.
//
// The page is revalidated on every load (no-cache with an ETag), so a new
// build is picked up at once and an unchanged one costs a 304. Chunk names
// carry a content hash, so browsers keep them for a year without asking.
func handler(fsys fs.FS, root string) gin.HandlerFunc {
	files := load(fsys, root)
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if (p != "/" && !strings.HasPrefix(p, assetsPrefix)) ||
			(c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			c.Next()
			return
		}
		c.Abort()
		f := files[p]
		if f == nil {
			c.Status(http.StatusNotFound)
			c.Writer.WriteHeaderNow()
			return
		}
		h := c.Writer.Header()
		h.Set("Content-Type", f.ctype)
		h.Set("ETag", f.etag)
		h.Set("Vary", "Accept-Encoding")
		h.Set("X-Content-Type-Options", "nosniff")
		if p == "/" {
			h.Set("Cache-Control", "no-cache")
			// The dashboard holds keyless admin actions, so no page may frame it (clickjacking).
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		} else {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if etagMatches(c.GetHeader("If-None-Match"), f.etag) {
			c.Status(http.StatusNotModified)
			c.Writer.WriteHeaderNow()
			return
		}
		body := f.body
		if f.gz != nil && acceptsGzip(c.Request.Header.Values("Accept-Encoding")) {
			body = f.gz
			h.Set("Content-Encoding", "gzip")
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		c.Status(http.StatusOK)
		if c.Request.Method == http.MethodGet {
			_, _ = c.Writer.Write(body)
		} else {
			c.Writer.WriteHeaderNow()
		}
	}
}

func etagMatches(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// acceptsGzip reports whether Accept-Encoding allows gzip, explicitly or by
// "*", with a nonzero quality.
func acceptsGzip(values []string) bool {
	allowed := false
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			name, params, _ := strings.Cut(strings.TrimSpace(token), ";")
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "gzip" && name != "*" {
				continue
			}
			q := 1.0
			if k, v, ok := strings.Cut(strings.TrimSpace(params), "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = parsed
				}
			}
			if name == "gzip" {
				return q > 0
			}
			allowed = q > 0
		}
	}
	return allowed
}
