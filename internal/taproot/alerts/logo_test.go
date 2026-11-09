package alerts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the repository root, seen from this package's directory.
const repoRoot = "../../.."

// TestLogoURLPointsAtCommittedPNG pins the logo's path: Discord fetches it
// from main, so a rename would break every alert's avatar and thumbnail
// without any error here.
func TestLogoURLPointsAtCommittedPNG(t *testing.T) {
	if logoPath != "internal/taproot/alerts/assets/taproot.png" {
		t.Fatalf("logoPath = %q; moving the logo breaks the avatar of messages already posted", logoPath)
	}
	if logoURL != "https://raw.githubusercontent.com/265866/taproot/main/"+logoPath {
		t.Fatalf("logoURL = %q", logoURL)
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(logoPath)))
	if err != nil {
		t.Fatalf("read logo: %v", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("logo is not a PNG: %v", err)
	}
	if cfg.Width != 256 || cfg.Height != 256 {
		t.Fatalf("logo is %dx%d, want 256x256", cfg.Width, cfg.Height)
	}
}

var faviconHref = regexp.MustCompile(`<link\s+rel="icon"\s+type="image/svg\+xml"\s+href="data:image/svg\+xml,([^"]+)"`)

// TestLogoMatchesFavicon fails when the favicon mark changes without a new
// PNG. web/tests/taprootMark.test.ts keeps index.html in sync with
// taprootMark.ts, which scripts/taproot/render-logo.ts renders.
func TestLogoMatchesFavicon(t *testing.T) {
	html, err := os.ReadFile(filepath.Join(repoRoot, "web", "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	m := faviconHref.FindSubmatch(html)
	if m == nil {
		t.Fatal("index.html has no SVG favicon data URL")
	}
	svg, err := url.PathUnescape(string(m[1]))
	if err != nil {
		t.Fatalf("decode favicon: %v", err)
	}
	recorded, err := os.ReadFile(filepath.Join("assets", "taproot.svg.sha256"))
	if err != nil {
		t.Fatalf("read recorded hash: %v", err)
	}
	sum := sha256.Sum256([]byte(svg))
	if got, want := hex.EncodeToString(sum[:]), strings.TrimSpace(string(recorded)); got != want {
		t.Fatalf("favicon SVG sha256 = %s, logo was rendered from %s; run `bun scripts/taproot/render-logo.ts` and commit the PNG", got, want)
	}
}
