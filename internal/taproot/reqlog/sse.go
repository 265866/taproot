package reqlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"
	"unicode/utf8"
)

// Streamed Anthropic replies are hundreds of delta events per response, each
// a JSON wrapper around a few bytes of text plus random space padding. The
// same text comes back whole in the next turn's request, but split across
// wrappers zstd cannot match it. The SSE transform moves every delta piece
// into one contiguous payload and the piece lengths and padding into a varint
// list, leaving a skeleton whose delta lines are identical to each other:
//
//	data: {"type":"content_block_delta","index":N,"delta":{"type":T,"F":"<piece>"<rest>}<spaces>}
//
// becomes the same line with an empty piece and no spaces. Every line of that
// shape is transformed, including ones whose piece is already empty, so
// decoding finds exactly the lines encoding changed. When every piece survives
// JSON unescaping and re-escaping unchanged, the payload holds unescaped text,
// which matches the next request more often.
const ssePrefix = `data: {"type":"content_block_delta","index":`

type sseParts struct {
	unescaped bool
	nums      []byte
	skel      []byte
	payload   []byte
}

type deltaLine struct {
	pieceStart, pieceEnd int
	// closeAt is the index of the '}' that closes the delta object.
	closeAt int
	spaces  int
}

func parseDelta(line []byte) (deltaLine, bool) {
	var d deltaLine
	if !bytes.HasPrefix(line, []byte(ssePrefix)) {
		return d, false
	}
	i := len(ssePrefix)
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	const deltaType = `,"delta":{"type":"`
	if !bytes.HasPrefix(line[i:], []byte(deltaType)) {
		return d, false
	}
	i += len(deltaType)
	j := bytes.IndexByte(line[i:], '"')
	if j < 0 || !bytes.HasPrefix(line[i+j:], []byte(`","`)) {
		return d, false
	}
	i += j + 3
	j = bytes.IndexByte(line[i:], '"')
	if j < 0 || !bytes.HasPrefix(line[i+j:], []byte(`":"`)) {
		return d, false
	}
	i += j + 3
	d.pieceStart = i
	for i < len(line) && line[i] != '"' {
		if line[i] == '\\' {
			i++
		}
		i++
	}
	if i >= len(line) {
		return d, false
	}
	d.pieceEnd = i
	i++
	k := bytes.IndexByte(line[i:], '}')
	if k < 0 || bytes.IndexByte(line[i:i+k], '{') >= 0 {
		return d, false
	}
	d.closeAt = i + k
	tail := line[d.closeAt+1:]
	n := 0
	for n < len(tail) && tail[n] == ' ' {
		n++
	}
	if string(tail[n:]) != "}" {
		return d, false
	}
	d.spaces = n
	return d, true
}

// encodeSSE transforms text and reports whether any delta line was found.
// Callers verify with decodeSSE before keeping the result.
func encodeSSE(text []byte) (sseParts, bool) {
	var p sseParts
	if !bytes.Contains(text, []byte(ssePrefix)) {
		return p, false
	}
	p.unescaped = true
	found := false
	for rest := text; len(rest) > 0; {
		line, next := cutLine(rest)
		rest = next
		if d, ok := parseDelta(trimNL(line)); ok {
			found = true
			piece := line[d.pieceStart:d.pieceEnd]
			if u, ok := unescapeJSON(piece); !ok || !bytes.Equal(escapeJSON(u), piece) {
				p.unescaped = false
				break
			}
		}
	}
	if !found {
		return p, false
	}
	var tmp [binary.MaxVarintLen64]byte
	p.skel = make([]byte, 0, len(text)/2)
	for rest := text; len(rest) > 0; {
		line, next := cutLine(rest)
		rest = next
		body := trimNL(line)
		d, ok := parseDelta(body)
		if !ok {
			p.skel = append(p.skel, line...)
			continue
		}
		piece := body[d.pieceStart:d.pieceEnd]
		if p.unescaped {
			piece, _ = unescapeJSON(piece)
		}
		p.nums = append(p.nums, tmp[:binary.PutUvarint(tmp[:], uint64(len(piece)))]...)
		p.nums = append(p.nums, tmp[:binary.PutUvarint(tmp[:], uint64(d.spaces))]...)
		p.payload = append(p.payload, piece...)
		p.skel = append(p.skel, body[:d.pieceStart]...)
		p.skel = append(p.skel, body[d.pieceEnd:d.closeAt+1]...)
		p.skel = append(p.skel, '}')
		p.skel = append(p.skel, line[len(body):]...)
	}
	return p, true
}

var errBadSSE = errors.New("malformed SSE transform")

func decodeSSE(p sseParts) ([]byte, error) {
	out := make([]byte, 0, len(p.skel)+len(p.payload)+len(p.nums)*8)
	nums, payload := p.nums, p.payload
	for rest := p.skel; len(rest) > 0; {
		line, next := cutLine(rest)
		rest = next
		body := trimNL(line)
		d, ok := parseDelta(body)
		if !ok {
			out = append(out, line...)
			continue
		}
		n, w := binary.Uvarint(nums)
		if w <= 0 {
			return nil, errBadSSE
		}
		nums = nums[w:]
		spaces, w := binary.Uvarint(nums)
		if w <= 0 || n > uint64(len(payload)) || spaces > 1<<16 {
			return nil, errBadSSE
		}
		nums = nums[w:]
		piece := payload[:n]
		payload = payload[n:]
		if p.unescaped {
			piece = escapeJSON(piece)
		}
		out = append(out, body[:d.pieceStart]...)
		out = append(out, piece...)
		out = append(out, body[d.pieceStart:d.closeAt+1]...)
		for range spaces {
			out = append(out, ' ')
		}
		out = append(out, '}')
		out = append(out, line[len(body):]...)
	}
	if len(nums) != 0 || len(payload) != 0 {
		return nil, errBadSSE
	}
	return out, nil
}

func cutLine(b []byte) (line, rest []byte) {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return b[:i+1], b[i+1:]
	}
	return b, nil
}

func trimNL(line []byte) []byte { return bytes.TrimSuffix(line, []byte("\n")) }

func unescapeJSON(s []byte) ([]byte, bool) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(s) {
			return nil, false
		}
		switch s[i] {
		case '"', '\\', '/':
			out = append(out, s[i])
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'u':
			if i+4 >= len(s) {
				return nil, false
			}
			v, err := strconv.ParseUint(string(s[i+1:i+5]), 16, 32)
			if err != nil {
				return nil, false
			}
			out = utf8.AppendRune(out, rune(v))
			i += 4
		default:
			return nil, false
		}
	}
	return out, true
}

// escapeJSON matches Anthropic's escaping: quote, backslash, and control
// characters; everything else raw. encodeSSE only unescapes when this
// reproduces every piece.
func escapeJSON(s []byte) []byte {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(s)+8)
	for _, c := range s {
		switch c {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		default:
			if c < 0x20 {
				out = append(out, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
			} else {
				out = append(out, c)
			}
		}
	}
	return out
}
