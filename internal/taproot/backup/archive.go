package backup

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	manifestVersion = 1
	manifestEntry   = "manifest.json"
	configEntry     = "config.yaml"
	taprootEntry    = "taproot.yaml"
	dbEntry         = "taproot.db"
	authPrefix      = "auth/"

	maxManifestBytes = 1 << 20
	// Config and auth files are read into memory on restore; this caps what a
	// damaged archive can make the server allocate.
	maxSmallFileBytes = 16 << 20
)

type manifest struct {
	Version   int            `json:"version"`
	ID        string         `json:"id"`
	CreatedAt time.Time      `json:"createdAt"`
	Reason    Reason         `json:"reason"`
	Files     []manifestFile `json:"files"`
}

type manifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// source is one archive member: in-memory data, or a file on disk for the DB snapshot.
type source struct {
	name string
	data []byte
	path string
}

func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errCorrupt, fmt.Sprintf(format, args...))
}

// validEntryName reports whether name is a member a backup may contain.
func validEntryName(name string) bool {
	switch name {
	case configEntry, taprootEntry, dbEntry:
		return true
	}
	rel, ok := strings.CutPrefix(name, authPrefix)
	if !ok || rel == "" || path.Clean(rel) != rel {
		return false
	}
	return filepath.IsLocal(filepath.FromSlash(rel))
}

// writeArchive hashes every source, then writes the manifest followed by the
// sources as a zstd-compressed tar at dest, filling in m.Files.
func writeArchive(dest string, m *manifest, sources []source) (err error) {
	m.Files = m.Files[:0]
	for _, s := range sources {
		size, sum, errHash := hashSource(s)
		if errHash != nil {
			return errHash
		}
		m.Files = append(m.Files, manifestFile{Path: s.name, Size: size, SHA256: sum})
	}
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil && err == nil {
			err = fmt.Errorf("close archive: %w", errClose)
		}
	}()
	zw, err := zstd.NewWriter(f)
	if err != nil {
		return fmt.Errorf("start zstd: %w", err)
	}
	tw := tar.NewWriter(zw)

	if err := writeMember(tw, manifestEntry, int64(len(manifestJSON)), m.CreatedAt, bytes.NewReader(manifestJSON)); err != nil {
		return err
	}
	for i, s := range sources {
		if err := copySource(tw, s, m.Files[i].Size, m.CreatedAt); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("finish tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish zstd: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync archive: %w", err)
	}
	return nil
}

func hashSource(s source) (int64, string, error) {
	if s.path == "" {
		sum := sha256.Sum256(s.data)
		return int64(len(s.data)), hex.EncodeToString(sum[:]), nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", s.name, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", fmt.Errorf("hash %s: %w", s.name, err)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func copySource(tw *tar.Writer, s source, size int64, modTime time.Time) error {
	if s.path == "" {
		return writeMember(tw, s.name, size, modTime, bytes.NewReader(s.data))
	}
	f, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open %s: %w", s.name, err)
	}
	defer func() { _ = f.Close() }()
	return writeMember(tw, s.name, size, modTime, f)
}

func writeMember(tw *tar.Writer, name string, size int64, modTime time.Time, r io.Reader) error {
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Size: size, Mode: 0o600, ModTime: modTime, Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write %s header: %w", name, err)
	}
	if _, err := io.CopyN(tw, r, size); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func openArchive(archivePath string) (*os.File, *zstd.Decoder, *tar.Reader, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open backup: %w", err)
	}
	zr, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		_ = f.Close()
		return nil, nil, nil, corruptf("zstd: %v", err)
	}
	return f, zr, tar.NewReader(zr), nil
}

// readManifestFrom reads and validates the manifest, which must be the first member.
func readManifestFrom(tr *tar.Reader) (manifest, error) {
	var m manifest
	hdr, err := tr.Next()
	if err != nil {
		return m, corruptf("read manifest: %v", err)
	}
	if hdr.Name != manifestEntry || hdr.Typeflag != tar.TypeReg || hdr.Size > maxManifestBytes {
		return m, corruptf("first member is not a manifest")
	}
	raw, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
	if err != nil {
		return m, corruptf("read manifest: %v", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, corruptf("parse manifest: %v", err)
	}
	if m.Version != manifestVersion {
		return m, corruptf("manifest version %d", m.Version)
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !validEntryName(f.Path) {
			return m, corruptf("manifest names unexpected file %q", f.Path)
		}
		if seen[f.Path] {
			return m, corruptf("manifest lists %q twice", f.Path)
		}
		seen[f.Path] = true
	}
	return m, nil
}

func readManifest(archivePath string) (manifest, error) {
	f, zr, tr, err := openArchive(archivePath)
	if err != nil {
		return manifest{}, err
	}
	defer func() {
		zr.Close()
		_ = f.Close()
	}()
	return readManifestFrom(tr)
}

// contents holds a verified archive's config and auth files. The DB member is
// streamed to the path passed to extractVerified instead.
type contents struct {
	manifest manifest
	files    map[string][]byte
	hasDB    bool
}

// extractVerified reads the whole archive, checks every member against the
// manifest, and writes the DB member to dbDest. On error dbDest may hold a
// partial file that the caller removes.
func extractVerified(archivePath, dbDest string) (contents, error) {
	f, zr, tr, err := openArchive(archivePath)
	if err != nil {
		return contents{}, err
	}
	defer func() {
		zr.Close()
		_ = f.Close()
	}()
	m, err := readManifestFrom(tr)
	if err != nil {
		return contents{}, err
	}
	want := make(map[string]manifestFile, len(m.Files))
	for _, mf := range m.Files {
		want[mf.Path] = mf
	}
	out := contents{manifest: m, files: map[string][]byte{}}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return contents{}, corruptf("read archive: %v", err)
		}
		mf, ok := want[hdr.Name]
		if !ok {
			return contents{}, corruptf("unexpected member %q", hdr.Name)
		}
		delete(want, hdr.Name)
		if hdr.Typeflag != tar.TypeReg || hdr.Size != mf.Size {
			return contents{}, corruptf("member %q does not match manifest", hdr.Name)
		}
		h := sha256.New()
		if hdr.Name == dbEntry {
			if err := streamTo(dbDest, io.TeeReader(tr, h)); err != nil {
				return contents{}, err
			}
			out.hasDB = true
		} else {
			if mf.Size > maxSmallFileBytes {
				return contents{}, corruptf("member %q too large", hdr.Name)
			}
			data, err := io.ReadAll(io.TeeReader(tr, h))
			if err != nil {
				return contents{}, corruptf("read %q: %v", hdr.Name, err)
			}
			out.files[hdr.Name] = data
		}
		if hex.EncodeToString(h.Sum(nil)) != mf.SHA256 {
			return contents{}, corruptf("checksum mismatch for %q", hdr.Name)
		}
	}
	for name := range want {
		return contents{}, corruptf("missing member %q", name)
	}
	if !out.hasDB {
		return contents{}, corruptf("no database snapshot")
	}
	return out, nil
}

func streamTo(dest string, r io.Reader) (err error) {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil && err == nil {
			err = fmt.Errorf("close %s: %w", dest, errClose)
		}
	}()
	if _, err := io.Copy(f, corruptOnError{r}); err != nil {
		if errors.Is(err, errCorrupt) {
			return err
		}
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dest, err)
	}
	return nil
}

// corruptOnError marks read failures as archive corruption, so they are
// told apart from write failures on the destination.
type corruptOnError struct{ r io.Reader }

func (c corruptOnError) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = corruptf("read database snapshot: %v", err)
	}
	return n, err
}
