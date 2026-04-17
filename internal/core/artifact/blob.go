package artifact

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// BlobStore manages content-addressed blob files on disk.
// Large artifact content is written once and referenced by its SHA-256 hash.
type BlobStore struct {
	root string // e.g. ~/.coden/workspace/<uuid>/artifacts/blobs
}

// NewBlobStore creates a BlobStore rooted at the given directory.
func NewBlobStore(root string) (*BlobStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("blob store mkdir: %w", err)
	}
	return &BlobStore{root: root}, nil
}

// Put writes content to the blob store and returns the blob ID (hex SHA-256)
// and whether the blob already existed on disk.
// If the blob already exists it is not overwritten.
func (bs *BlobStore) Put(data []byte) (blobID string, existed bool, err error) {
	h := sha256.Sum256(data)
	blobID = fmt.Sprintf("%x", h)

	dir := bs.blobDir(blobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("blob mkdir: %w", err)
	}

	path := bs.blobPath(blobID)
	// Skip write if already exists (content-addressed = idempotent).
	if _, err := os.Stat(path); err == nil {
		return blobID, true, nil
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", false, fmt.Errorf("blob write: %w", err)
	}
	return blobID, false, nil
}

// Get reads the content of a blob by ID.
func (bs *BlobStore) Get(blobID string) ([]byte, error) {
	path := bs.blobPath(blobID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("blob read %s: %w", blobID, err)
	}
	return data, nil
}

// Delete removes a blob file. No error if the blob does not exist.
func (bs *BlobStore) Delete(blobID string) error {
	path := bs.blobPath(blobID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("blob delete %s: %w", blobID, err)
	}
	return nil
}

// Exists returns true if a blob with the given ID exists on disk.
func (bs *BlobStore) Exists(blobID string) bool {
	_, err := os.Stat(bs.blobPath(blobID))
	return err == nil
}

// blobDir returns the two-character prefix directory for fan-out.
func (bs *BlobStore) blobDir(blobID string) string {
	prefix := blobID
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(bs.root, prefix)
}

// blobPath returns the full filesystem path for a blob.
func (bs *BlobStore) blobPath(blobID string) string {
	return filepath.Join(bs.blobDir(blobID), blobID)
}
