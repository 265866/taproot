package taproot

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/logging"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// benchBody is a small Claude request: the model first, then about 8 KiB of
// messages.
var benchBody = []byte(`{"model":"claude-opus-5-5","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"` +
	strings.Repeat("lorem ipsum ", 700) + `"}]}`)

// benchEngine serves a streamed reply of 20 events behind upstream's
// request-ID middleware, with or without clientRequests, whose writer runs
// and writes to a real store.
func benchEngine(b *testing.B, record bool) (*gin.Engine, func() int) {
	b.Helper()
	gin.SetMode(gin.ReleaseMode)
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(nil) })
	e := gin.New()
	e.Use(logging.GinLogrusLogger(), logging.GinLogrusRecovery())
	rows := func() int { return 0 }
	if record {
		db, err := store.Open(context.Background(), store.Config{}, b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		sink, err := ingest.New(ingest.Config{PollInterval: "0"}, db)
		if err != nil {
			b.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		sink.Start(ctx)
		e.Use(clientRequests(sink))
		rows = func() int {
			cancel()
			sink.Wait()
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM client_requests`).Scan(&n); err != nil {
				b.Fatal(err)
			}
			_ = db.Close()
			return n
		}
	}
	event := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"" + strings.Repeat("x", 80) + "\"}}\n\n")
	e.POST("/v1/messages", func(c *gin.Context) {
		if _, err := io.Copy(io.Discard, c.Request.Body); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		for range 20 {
			_, _ = c.Writer.Write(event)
			c.Writer.Flush()
		}
	})
	return e, rows
}

func checkRows(b *testing.B, rows func() int, sent int, record bool) {
	b.Helper()
	if n := rows(); record && n != sent {
		b.Fatalf("recorded %d of %d requests", n, sent)
	}
}

// BenchmarkClientRequests measures the middleware in process (engine) and
// over a loopback HTTP connection (http), with recording off and on.
func BenchmarkClientRequests(b *testing.B) {
	for _, mode := range []struct {
		name   string
		record bool
	}{{"off", false}, {"on", true}} {
		b.Run("engine/"+mode.name, func(b *testing.B) {
			e, rows := benchEngine(b, mode.record)
			sent := 0
			b.ReportAllocs()
			for b.Loop() {
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(benchBody)))
				if rec.Code != http.StatusOK {
					b.Fatalf("status %d", rec.Code)
				}
				sent++
			}
			b.StopTimer()
			checkRows(b, rows, sent, mode.record)
		})
		b.Run("http/"+mode.name, func(b *testing.B) {
			e, rows := benchEngine(b, mode.record)
			srv := httptest.NewServer(e)
			defer srv.Close()
			client := srv.Client()
			sent := 0
			for b.Loop() {
				resp, err := client.Post(srv.URL+"/v1/messages", "application/json", bytes.NewReader(benchBody))
				if err != nil {
					b.Fatal(err)
				}
				n, err := io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if err != nil || resp.StatusCode != http.StatusOK || n < 20*100 {
					b.Fatalf("status %d, %d bytes, %v", resp.StatusCode, n, err)
				}
				sent++
			}
			b.StopTimer()
			checkRows(b, rows, sent, mode.record)
		})
	}
}
