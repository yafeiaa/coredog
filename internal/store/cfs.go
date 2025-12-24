package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
)

// CFSStore implements the Store interface for CFS (Cloud File System)
type CFSStore struct {
	MountPath string
	StoreDir  string
}

// Upload uploads the corefile to the CFS mount point
// Returns the local file path (since CFS is a mounted filesystem)
func (cs *CFSStore) Upload(ctx context.Context, path string) (downloadurl string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.Wrap(err, "failed to open corefile")
	}
	defer f.Close()

	// Create the destination path
	_, filename := filepath.Split(path)
	destDir := filepath.Join(cs.MountPath, cs.StoreDir)

	// Ensure destination directory exists
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create destination directory")
	}

	destPath := filepath.Join(destDir, filename)

	// Create the destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to create destination file")
	}
	defer destFile.Close()

	// Copy the file content
	if _, err := io.Copy(destFile, f); err != nil {
		return "", errors.Wrap(err, "failed to copy file to CFS")
	}

	// Ensure file is synced to disk
	if err := destFile.Sync(); err != nil {
		return "", errors.Wrap(err, "failed to sync file to CFS")
	}

	// Return the CFS path as download URL
	// Typically: cfs://mount-id/storeDir/filename or file path
	downloadurl = fmt.Sprintf("cfs://%s/%s", destDir, filename)

	return downloadurl, nil
}

// NewCFSStore creates a new CFS store instance
func NewCFSStore(mountPath, storedir string) (Store, error) {
	// Validate mount path exists and is accessible
	info, err := os.Stat(mountPath)
	if err != nil {
		return nil, errors.Wrapf(err, "CFS mount path %s is not accessible", mountPath)
	}
	if !info.IsDir() {
		return nil, errors.Errorf("CFS mount path %s is not a directory", mountPath)
	}

	// Test write permission
	testFile := filepath.Join(mountPath, ".coredog_write_test_"+fmt.Sprintf("%d", time.Now().Unix()))
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return nil, errors.Wrapf(err, "CFS mount path %s is not writable", mountPath)
	}
	os.Remove(testFile)

	return &CFSStore{
		MountPath: mountPath,
		StoreDir:  storedir,
	}, nil
}
