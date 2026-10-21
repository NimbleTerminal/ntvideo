package ntvideo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Limits bound native local inputs and headless frame output. Zero or negative
// fields use defaults. These checks are not a decoder sandbox or a hard bound on
// libmpv/FFmpeg's internal allocations; isolate decoding for hostile workloads.
type Limits struct {
	MaxFileBytes        int64
	MaxWidth, MaxHeight int
	MaxPixels           int64
}

func (l Limits) normalized() Limits {
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = 2 << 30
	}
	if l.MaxWidth <= 0 {
		l.MaxWidth = 4096
	}
	if l.MaxHeight <= 0 {
		l.MaxHeight = 4096
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = 4096 * 2160
	}
	return l
}
func validateLocalFile(path string, limits Limits) (string, error) {
	// Reject URLs, mpv pseudo-protocols, UNC/device paths and embedded NULs.
	if path == "" || strings.ContainsRune(path, 0) || strings.Contains(path, "://") || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
		return "", fmt.Errorf("ntvideo: local regular files only: %q", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("ntvideo: source is not a regular file")
	}
	if info.Size() > limits.normalized().MaxFileBytes {
		return "", fmt.Errorf("ntvideo: file exceeds size limit")
	}
	return resolved, nil
}
func checkDimensions(w, h int, l Limits) error {
	l = l.normalized()
	if w < 0 || h < 0 || w > l.MaxWidth || h > l.MaxHeight || int64(w)*int64(h) > l.MaxPixels {
		return fmt.Errorf("ntvideo: video dimensions %dx%d exceed limits", w, h)
	}
	return nil
}
