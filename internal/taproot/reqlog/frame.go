package reqlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// A frame is one record in a segment stream:
//
//	TPRL1 <meta length> <text length>\n<meta JSON><text>\n
//
// The meta JSON repeats the index row, so an open segment can be re-indexed
// from its own bytes after a crash.
const frameMagic = "TPRL1 "

// maxFrameHeader bounds the header line so a corrupt tail cannot make
// recovery read an unbounded line.
const maxFrameHeader = 64

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
	RequestHeaders  map[string][]string `json:"requestHeaders,omitempty"`
	ResponseHeaders map[string][]string `json:"responseHeaders,omitempty"`
	Sections        map[string]span     `json:"sections,omitempty"`
	APIErrors       []apiErrorView      `json:"apiErrors,omitempty"`
}

type apiErrorView struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// frame keeps the text apart from the header so a large record is not copied
// again just to prepend a few hundred bytes.
type frame struct {
	head []byte
	text []byte
}

func (f frame) size() int { return len(f.head) + len(f.text) + 1 }

func (f frame) writeTo(w io.Writer) error {
	for _, part := range [][]byte{f.head, f.text, {'\n'}} {
		if _, err := w.Write(part); err != nil {
			return err
		}
	}
	return nil
}

func encodeFrame(meta *recordMeta, text []byte) (frame, error) {
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return frame{}, fmt.Errorf("encode record meta: %w", err)
	}
	head := make([]byte, 0, len(frameMagic)+24+len(metaJSON))
	head = append(head, frameMagic...)
	head = strconv.AppendInt(head, int64(len(metaJSON)), 10)
	head = append(head, ' ')
	head = strconv.AppendInt(head, int64(len(text)), 10)
	head = append(head, '\n')
	head = append(head, metaJSON...)
	return frame{head: head, text: text}, nil
}

var errBadFrame = errors.New("malformed request log frame")

// decodeFrame splits one complete frame.
func decodeFrame(frame []byte) (*recordMeta, []byte, error) {
	nl := bytes.IndexByte(frame, '\n')
	if nl < 0 || nl > maxFrameHeader {
		return nil, nil, errBadFrame
	}
	metaLen, textLen, err := parseFrameHeader(frame[:nl])
	if err != nil {
		return nil, nil, err
	}
	body := frame[nl+1:]
	if len(body) != metaLen+textLen+1 || body[len(body)-1] != '\n' {
		return nil, nil, errBadFrame
	}
	var meta recordMeta
	if err := json.Unmarshal(body[:metaLen], &meta); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errBadFrame, err)
	}
	return &meta, body[metaLen : metaLen+textLen], nil
}

func parseFrameHeader(line []byte) (int, int, error) {
	if !bytes.HasPrefix(line, []byte(frameMagic)) {
		return 0, 0, errBadFrame
	}
	fields := bytes.Fields(line[len(frameMagic):])
	if len(fields) != 2 {
		return 0, 0, errBadFrame
	}
	metaLen, errMeta := strconv.Atoi(string(fields[0]))
	textLen, errText := strconv.Atoi(string(fields[1]))
	if errMeta != nil || errText != nil || metaLen <= 0 || textLen < 0 {
		return 0, 0, errBadFrame
	}
	return metaLen, textLen, nil
}

// scanFrames walks complete frames from r, starting at offset base, and
// returns where the last complete frame ended. A torn or corrupt tail stops
// the scan without an error; only read failures are errors.
func scanFrames(r io.Reader, base int64, visit func(offset int64, length int, meta *recordMeta)) (int64, error) {
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
		metaLen, textLen, errHeader := parseFrameHeader(bytes.TrimSuffix(line, []byte("\n")))
		if errHeader != nil {
			return pos, nil
		}
		headerLen := len(line)
		metaJSON := make([]byte, metaLen)
		if _, err := io.ReadFull(br, metaJSON); err != nil {
			return pos, ignoreEOF(err)
		}
		var meta recordMeta
		if json.Unmarshal(metaJSON, &meta) != nil {
			return pos, nil
		}
		if _, err := br.Discard(textLen); err != nil {
			return pos, ignoreEOF(err)
		}
		last, err := br.ReadByte()
		if err != nil {
			return pos, ignoreEOF(err)
		}
		if last != '\n' {
			return pos, nil
		}
		length := headerLen + metaLen + textLen + 1
		visit(pos, length, &meta)
		pos += int64(length)
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}
