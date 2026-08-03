package importer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DirFileStore keeps raw uploads under <root>/<user>/<sha256>, so a batch can be
// reprocessed after an importer fix without re-exporting from the phone.
// Files are plaintext, consistent with docs/adr/0002-no-encryption.md.
type DirFileStore struct{ root string }

// NewDirFileStore builds a file store rooted at dir.
func NewDirFileStore(dir string) *DirFileStore { return &DirFileStore{root: dir} }

// hexName matches a sha256 in hex. The stored name is derived, never
// user-supplied, and this check keeps it that way even if a caller changes.
var hexName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Put writes the upload and returns its path.
func (s *DirFileStore) Put(userID int64, sha256 string, data []byte) (string, error) {
	if !hexName.MatchString(sha256) {
		return "", fmt.Errorf("importer: refusing to store upload under %q", sha256)
	}
	dir := filepath.Join(s.root, strconv.FormatInt(userID, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("importer: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, sha256)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("importer: writing %s: %w", path, err)
	}
	return path, nil
}

// Open reads a retained upload back.
func (s *DirFileStore) Open(path string) (io.ReadCloser, error) {
	// The path came from Put, which builds it from the root and a hex digest;
	// containment is re-checked so a stored value tampered with in the database
	// cannot read an arbitrary file.
	root, err := filepath.Abs(s.root)
	if err != nil {
		return nil, fmt.Errorf("importer: resolving upload root: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("importer: resolving %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("importer: %s is outside the upload directory", path)
	}
	f, err := os.Open(abs) //nolint:gosec // path is contained in the upload root, checked above
	if err != nil {
		return nil, fmt.Errorf("importer: opening retained upload: %w", err)
	}
	return f, nil
}
