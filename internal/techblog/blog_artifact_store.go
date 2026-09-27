package techblog

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var safePathComponentPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// BlogArtifactStore persists the self-contained blog HTML artifact so it can
// be published later. Implementations must be idempotent: writing the same
// Flow ID and file name again replaces the previous artifact.
type BlogArtifactStore interface {
	WriteBlogArtifact(flowID string, fileName string, html string) (string, error)
}

// DirectoryBlogArtifactStore writes artifacts below one root directory as
// <root>/<flow-id>/<file-name>.
type DirectoryBlogArtifactStore struct {
	rootDirectory string
}

// NewDirectoryBlogArtifactStore returns a store rooted at the absolute form of
// rootDirectory.
func NewDirectoryBlogArtifactStore(rootDirectory string) (DirectoryBlogArtifactStore, error) {
	absoluteRoot, err := filepath.Abs(rootDirectory)
	if err != nil {
		return DirectoryBlogArtifactStore{}, fmt.Errorf("resolve blog artifact directory: %w", err)
	}
	return DirectoryBlogArtifactStore{rootDirectory: absoluteRoot}, nil
}

// WriteBlogArtifact atomically writes html and returns the artifact's absolute
// path.
func (store DirectoryBlogArtifactStore) WriteBlogArtifact(flowID string, fileName string, html string) (string, error) {
	directoryName := safePathComponentPattern.ReplaceAllString(flowID, "-")
	baseName := safePathComponentPattern.ReplaceAllString(filepath.Base(fileName), "-")
	if directoryName == "" || directoryName == "." || directoryName == ".." || baseName == "" || baseName == "." || baseName == ".." {
		return "", fmt.Errorf("blog artifact path components are invalid")
	}
	directory := filepath.Join(store.rootDirectory, directoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create blog artifact directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+baseName+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary blog artifact: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.WriteString(html); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("write blog artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync blog artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close blog artifact: %w", err)
	}
	if err := os.Chmod(temporaryName, 0o644); err != nil {
		return "", fmt.Errorf("set blog artifact permissions: %w", err)
	}
	destination := filepath.Join(directory, baseName)
	if err := os.Rename(temporaryName, destination); err != nil {
		return "", fmt.Errorf("publish blog artifact: %w", err)
	}
	return destination, nil
}
