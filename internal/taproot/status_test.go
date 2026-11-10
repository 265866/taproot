package taproot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Through the real server: the status, attention, and pools endpoints are
// mounted, refuse a device without Admin, and read the auth manager's
// accounts and the client requests the middleware recorded.
func TestStatusEndpointsThroughTheServer(t *testing.T) {
	upstream := fakeUpstream(t)
	f := fixture{h: newProxyServer(t, upstream.URL)}
	body := fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, claudeModel)
	if rec := f.do(req{method: http.MethodPost, remote: adminIP, path: "/v1/messages", body: body, headers: map[string]string{"Content-Type": "application/json"}}); rec.Code != http.StatusOK {
		t.Fatalf("proxy request = %d %.200s", rec.Code, rec.Body)
	}

	for _, path := range []string{"/taproot/api/v1/status", "/taproot/api/v1/attention", "/taproot/api/v1/pools"} {
		if rec := f.do(req{remote: workerIP, path: path}); rec.Code != http.StatusForbidden {
			t.Errorf("%s from a device without Admin = %d, want 403", path, rec.Code)
		}
		if rec := f.do(req{remote: adminIP, path: path}); rec.Code != http.StatusOK {
			t.Errorf("%s from an admin = %d %.200s", path, rec.Code, rec.Body)
		}
	}

	var st struct {
		State     string `json:"state"`
		Headline  string `json:"headline"`
		Providers []struct {
			Provider       string `json:"provider"`
			Usable         int    `json:"usable"`
			RecentRequests int64  `json:"recentRequests"`
		} `json:"providers"`
		Window struct {
			Requests int64 `json:"requests"`
		} `json:"window"`
	}
	// The request is written once its late usage records had time to arrive.
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := f.do(req{remote: adminIP, path: "/taproot/api/v1/status"})
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode status: %v: %.200s", err, rec.Body)
		}
		if st.Window.Requests > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.State != "normal" || st.Headline != "Serving normally" || st.Window.Requests != 1 {
		t.Fatalf("status = %+v", st)
	}
	if len(st.Providers) != 2 || st.Providers[0].Provider != "claude" || st.Providers[0].Usable != 1 || st.Providers[0].RecentRequests != 1 || st.Providers[1].Provider != "codex" {
		t.Errorf("providers = %+v", st.Providers)
	}
}
