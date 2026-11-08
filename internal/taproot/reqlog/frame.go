package reqlog

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
)

// A frame is one record in a segment stream:
//
//	TPRL2 <flags> <meta> <refs> <nums> <skel> <payload>\n<meta JSON><refs><nums><skel><payload>\n
//
// The header gives each part's length. The record text is the skeleton, or
// the SSE transform of nums, skeleton, and payload when flags has frameSSE
// (with frameUnescaped, the payload holds JSON-unescaped pieces), with every
// blob reference spliced back in as base64. Refs are
// (uvarint position delta, SHA-256 of the decoded blob) pairs, positions
// counted in the text before splicing.
//
// The meta JSON repeats the index row, so an open segment can be re-indexed
// from its own bytes after a crash.
const frameMagic = "TPRL2 "

const (
	frameSSE       = 1
	frameUnescaped = 2
)

// maxFrameHeader bounds the header line so a corrupt tail cannot make
// recovery read an unbounded line.
const maxFrameHeader = 128

// maxFramePart bounds every length a header declares, far above any real
// record, so the parts sum in an int64 without overflowing.
const maxFramePart int64 = 1 << 40

// maxFrameMeta bounds the meta JSON a header may declare. Real meta is the
// index row plus headers, a few KiB; a larger value is corruption and must not
// become an allocation.
const maxFrameMeta = 16 << 20

// recordMeta is the index row plus what the detail view needs that the text
// layout would make awkward to parse back.
type recordMeta struct {
	RequestID       string              `json:"requestId"`
	TraceID         string              `json:"traceId,omitempty"`
	SessionID       string              `json:"sessionId,omitempty"`
	ParentSessionID string              `json:"parentSessionId,omitempty"`
	TimeMs          int64               `json:"ts"`
	DurationMs      int64               `json:"durationMs"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	Path            string              `json:"path"`
	Status          int                 `json:"status"`
	Model           string              `json:"model,omitempty"`
	AuthIndex       string              `json:"authIndex,omitempty"`
	Streaming       bool                `json:"streaming,omitempty"`
	ErrorOnly       bool                `json:"errorOnly,omitempty"`
	Dropped         string              `json:"dropped,omitempty"`
	RequestBytes    int                 `json:"requestBytes"`
	ResponseBytes   int                 `json:"responseBytes"`
	TextBytes       int                 `json:"textBytes"`
	RequestHeaders  map[string][]string `json:"requestHeaders,omitempty"`
	ResponseHeaders map[string][]string `json:"responseHeaders,omitempty"`
	Sections        map[string]span     `json:"sections,omitempty"`
	APIErrors       []apiErrorView      `json:"apiErrors,omitempty"`
}

type apiErrorView struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type blobRef struct {
	pos  int
	hash blobHash
}

// frame keeps its parts apart so a large record is not copied again just to
// prepend a header.
type frame struct {
	parts [][]byte
}

func (f frame) size() int {
	n := 0
	for _, p := range f.parts {
		n += len(p)
	}
	return n
}

func (f frame) writeTo(w io.Writer) error {
	for _, part := range f.parts {
		if len(part) == 0 {
			continue
		}
		if _, err := w.Write(part); err != nil {
			return err
		}
	}
	return nil
}

func (f frame) bytes() []byte {
	out := make([]byte, 0, f.size())
	for _, p := range f.parts {
		out = append(out, p...)
	}
	return out
}

// encodeFrame stores text as residual plus blob refs. onBlob sees every blob
// occurrence, repeats included.
func encodeFrame(meta *recordMeta, text []byte, onBlob func(blobHash, []byte)) (frame, []blobRef, error) {
	meta.TextBytes = len(text)
	residual, refs := splitBlobs(text, onBlob)
	flags := 0
	skel := residual
	var nums, payload []byte
	if parts, ok := encodeSSE(residual); ok {
		if back, err := decodeSSE(parts); err == nil && bytes.Equal(back, residual) {
			flags = frameSSE
			if parts.unescaped {
				flags |= frameUnescaped
			}
			nums, skel, payload = parts.nums, parts.skel, parts.payload
		}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return frame{}, nil, fmt.Errorf("encode record meta: %w", err)
	}
	refsBin := encodeRefs(refs)
	head := make([]byte, 0, len(frameMagic)+64)
	head = append(head, frameMagic...)
	for i, n := range []int{flags, len(metaJSON), len(refsBin), len(nums), len(skel), len(payload)} {
		if i > 0 {
			head = append(head, ' ')
		}
		head = strconv.AppendInt(head, int64(n), 10)
	}
	head = append(head, '\n')
	return frame{parts: [][]byte{head, metaJSON, refsBin, nums, skel, payload, {'\n'}}}, refs, nil
}

func encodeRefs(refs []blobRef) []byte {
	if len(refs) == 0 {
		return nil
	}
	out := make([]byte, 0, len(refs)*34)
	var tmp [binary.MaxVarintLen64]byte
	prev := 0
	for _, r := range refs {
		out = append(out, tmp[:binary.PutUvarint(tmp[:], uint64(r.pos-prev))]...)
		out = append(out, r.hash[:]...)
		prev = r.pos
	}
	return out
}

func decodeRefs(b []byte, textLen int) ([]blobRef, error) {
	var refs []blobRef
	pos := 0
	for len(b) > 0 {
		d, w := binary.Uvarint(b)
		if w <= 0 || len(b) < w+len(blobHash{}) || d > uint64(textLen-pos) {
			return nil, errBadFrame
		}
		pos += int(d)
		refs = append(refs, blobRef{pos: pos, hash: blobHash(b[w : w+len(blobHash{})])})
		b = b[w+len(blobHash{}):]
	}
	return refs, nil
}

var errBadFrame = errors.New("malformed request log frame")

type frameHeader struct {
	flags                                 int
	meta, refs, nums, skel, payload, size int
}

func parseFrameHeader(line []byte) (frameHeader, error) {
	var h frameHeader
	if !bytes.HasPrefix(line, []byte(frameMagic)) {
		return h, errBadFrame
	}
	fields := bytes.Fields(line[len(frameMagic):])
	if len(fields) != 6 {
		return h, errBadFrame
	}
	var v [6]int
	total := int64(len(line)) + 2
	for i, f := range fields {
		n, err := strconv.ParseInt(string(f), 10, 64)
		if err != nil || n < 0 || n > maxFramePart || n > math.MaxInt {
			return h, errBadFrame
		}
		v[i] = int(n)
		if i > 0 {
			total += n
		}
	}
	// A frame must fit the platform's int to be sliced.
	if total > math.MaxInt {
		return h, errBadFrame
	}
	h = frameHeader{flags: v[0], meta: v[1], refs: v[2], nums: v[3], skel: v[4], payload: v[5], size: int(total)}
	if h.meta == 0 || h.meta > maxFrameMeta || h.flags&^(frameSSE|frameUnescaped) != 0 {
		return h, errBadFrame
	}
	return h, nil
}

// decodeFrame splits one complete frame into its meta, the record text
// before blob splicing, and the blob refs into that text.
func decodeFrame(fr []byte) (*recordMeta, []byte, []blobRef, error) {
	nl := bytes.IndexByte(fr, '\n')
	if nl < 0 || nl > maxFrameHeader {
		return nil, nil, nil, errBadFrame
	}
	h, err := parseFrameHeader(fr[:nl])
	if err != nil {
		return nil, nil, nil, err
	}
	if h.size != len(fr) || fr[len(fr)-1] != '\n' {
		return nil, nil, nil, errBadFrame
	}
	p := nl + 1
	take := func(n int) []byte {
		b := fr[p : p+n]
		p += n
		return b
	}
	metaJSON, refsBin, nums, skel, payload := take(h.meta), take(h.refs), take(h.nums), take(h.skel), take(h.payload)
	var meta recordMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", errBadFrame, err)
	}
	text := skel
	if h.flags&frameSSE != 0 {
		text, err = decodeSSE(sseParts{unescaped: h.flags&frameUnescaped != 0, nums: nums, skel: skel, payload: payload})
		if err != nil {
			return nil, nil, nil, err
		}
	}
	refs, err := decodeRefs(refsBin, len(text))
	if err != nil {
		return nil, nil, nil, err
	}
	return &meta, text, refs, nil
}

// scanFrames walks complete frames from r, starting at offset base, and
// returns where the last complete frame ended. end is where r's data stops; a
// header declaring more than that is a torn or corrupt tail. Such a tail stops
// the scan without an error; only read failures are errors.
func scanFrames(r io.Reader, base, end int64, visit func(offset int64, length int, meta *recordMeta, refs []blobHash)) (int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	pos := base
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, bufio.ErrBufferFull) {
				return pos, nil
			}
			return pos, err
		}
		if len(line) > maxFrameHeader {
			return pos, nil
		}
		h, errHeader := parseFrameHeader(bytes.TrimSuffix(line, []byte("\n")))
		if errHeader != nil || int64(h.size) > end-pos {
			return pos, nil
		}
		metaJSON := make([]byte, h.meta)
		if _, err := io.ReadFull(br, metaJSON); err != nil {
			return pos, ignoreEOF(err)
		}
		var meta recordMeta
		if json.Unmarshal(metaJSON, &meta) != nil {
			return pos, nil
		}
		refsBin := make([]byte, h.refs)
		if _, err := io.ReadFull(br, refsBin); err != nil {
			return pos, ignoreEOF(err)
		}
		hashes, ok := refHashes(refsBin)
		if !ok {
			return pos, nil
		}
		if _, err := br.Discard(h.nums + h.skel + h.payload); err != nil {
			return pos, ignoreEOF(err)
		}
		last, err := br.ReadByte()
		if err != nil {
			return pos, ignoreEOF(err)
		}
		if last != '\n' {
			return pos, nil
		}
		visit(pos, h.size, &meta, hashes)
		pos += int64(h.size)
	}
}

// refHashes lists the blob hashes in a refs section without positions.
func refHashes(b []byte) ([]blobHash, bool) {
	var out []blobHash
	for len(b) > 0 {
		_, w := binary.Uvarint(b)
		if w <= 0 || len(b) < w+len(blobHash{}) {
			return nil, false
		}
		out = append(out, blobHash(b[w:w+len(blobHash{})]))
		b = b[w+len(blobHash{}):]
	}
	return out, true
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}
