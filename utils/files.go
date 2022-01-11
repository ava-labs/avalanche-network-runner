package utils

import (
	"io"
	"os"
)

const (
	filePerms = 0755
)

// CopyFile is a helper to copy a file from src to dst
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	// Grant permission to copy
	if err := os.Chmod(dst, filePerms); err != nil {
		return err
	}

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
