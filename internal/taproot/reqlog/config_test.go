package reqlog

import (
	"testing"
	"time"
)

func TestParseByteSize(t *testing.T) {
	for raw, want := range map[string]ByteSize{
		"1073741824": 1 << 30,
		"1GB":        1 << 30,
		"1GiB":       1 << 30,
		"512mb":      512 << 20,
		"1.5GiB":     3 << 29,
		"0.5KB":      512,
		"-1":         -1,
		"0":          0,
		"9e18":       9e18,
	} {
		got, err := ParseByteSize(raw)
		if err != nil || got != want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"NaN", "Inf", "-Inf", "1e30", "9.3e18", "8388608TB", "0.5", "1e-10GB", "lots", ""} {
		if got, err := ParseByteSize(raw); err == nil {
			t.Errorf("ParseByteSize(%q) = %d, want an error", raw, got)
		}
	}
}

// A max-days past what a time.Duration holds overflowed the age cutoff into
// the future, so the next retention pass dropped every record.
func TestMaxDaysPastDurationRangeIsRefused(t *testing.T) {
	if err := (Config{MaxDays: maxRetentionDays + 1}).validate(); err == nil {
		t.Fatalf("max-days %d accepted", maxRetentionDays+1)
	}
	if err := (Config{MaxDays: maxRetentionDays}).validate(); err != nil {
		t.Fatalf("max-days %d refused: %v", maxRetentionDays, err)
	}
	if cutoff := -time.Duration(maxRetentionDays) * 24 * time.Hour; cutoff >= 0 {
		t.Fatalf("cutoff for the largest allowed max-days is %v, want negative", cutoff)
	}
}
