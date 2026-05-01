//go:build !darwin || !arm64

package io

import (
	"io"
	"os"
)

// isAppleSilicon always returns false on non-Apple platforms.
func isAppleSilicon() bool {
	return false
}

// supportsAMX returns false on non-Apple platforms.
func supportsAMX() bool {
	return false
}

// detectChipInfo fills in minimal info for non-Apple platforms.
func detectChipInfo(info *AppleMInfo) {
	// Leave default values
	info.OptimalBufferSize = DefaultBufferSize
	info.OptimalChunkSize = 512 * 1024
}

// CopyFile copies a file using standard methods.
func CopyFile(src, dst string) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}
	defer dstFile.Close()

	return io.Copy(dstFile, srcFile)
}

// MMapReadOnly creates a read-only memory mapping.
func MMapReadOnly(path string) ([]byte, *os.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	// Return a dummy file handle for consistency
	f, _ := os.Open(path)
	return data, f, nil
}

// Munmap releases resources.
func Munmap(data []byte, f *os.File) error {
	return f.Close()
}
