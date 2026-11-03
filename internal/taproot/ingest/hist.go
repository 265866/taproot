package ingest

import (
	"encoding/binary"
	"math"
	"sort"
)

// histogram is a log-bucketed millisecond histogram. Bucket 0 holds values
// <= 0; bucket i >= 1 holds [histBase^(i-1), histBase^i). Percentiles are
// therefore within about 5% of the exact value, which keeps hourly rollups
// mergeable without storing every sample.
type histogram map[int]uint64

const histBase = 1.1

var histLogBase = math.Log(histBase)

func histBucket(ms int64) int {
	if ms <= 0 {
		return 0
	}
	return int(math.Floor(math.Log(float64(ms))/histLogBase)) + 1
}

func (h histogram) add(ms int64) { h[histBucket(ms)]++ }

func (h histogram) merge(o histogram) {
	for k, v := range o {
		h[k] += v
	}
}

func (h histogram) count() uint64 {
	var n uint64
	for _, v := range h {
		n += v
	}
	return n
}

// percentile returns the geometric midpoint of the bucket holding the p-th
// sample, or nil for an empty histogram.
func (h histogram) percentile(p float64) *float64 {
	n := h.count()
	if n == 0 {
		return nil
	}
	rank := uint64(math.Ceil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	keys := make([]int, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var seen uint64
	for _, k := range keys {
		seen += h[k]
		if seen >= rank {
			v := 0.0
			if k > 0 {
				v = math.Round(math.Pow(histBase, float64(k)-0.5))
			}
			return &v
		}
	}
	return nil
}

// encode writes sorted (bucket, count) uvarint pairs.
func (h histogram) encode() []byte {
	if len(h) == 0 {
		return nil
	}
	keys := make([]int, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	buf := make([]byte, 0, len(keys)*4)
	for _, k := range keys {
		buf = binary.AppendUvarint(buf, uint64(k))
		buf = binary.AppendUvarint(buf, h[k])
	}
	return buf
}

func decodeHistogram(b []byte, into histogram) {
	for len(b) > 0 {
		k, n := binary.Uvarint(b)
		if n <= 0 {
			return
		}
		b = b[n:]
		v, m := binary.Uvarint(b)
		if m <= 0 {
			return
		}
		b = b[m:]
		into[int(k)] += v
	}
}
