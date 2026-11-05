package ntvideo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const workerSettingsName = "ntvideo-settings.json"
const maxWorkerSettingsBytes = 16 << 10

type workerSettings struct {
	Version     int    `json:"version"`
	LibraryPath string `json:"library_path"`
}

// The settings file is written by the host in its private staging directory.
// Library paths are trusted host configuration, never media-derived metadata.
func resolveWorkerLibrary(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("ntvideo: resolve worker libmpv: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("ntvideo: worker libmpv must be a regular file")
	}
	return resolved, nil
}

func writeWorkerSettings(dir, path string) error {
	resolved, err := resolveWorkerLibrary(path)
	if err != nil {
		return err
	}
	if resolved == "" {
		return nil
	}
	data, err := json.Marshal(workerSettings{Version: 1, LibraryPath: resolved})
	if err != nil {
		return err
	}
	if len(data) > maxWorkerSettingsBytes {
		return errors.New("ntvideo: worker settings exceed limit")
	}
	return os.WriteFile(filepath.Join(dir, workerSettingsName), data, 0600)
}

func workerLibraryPath(dir, fallback string) (string, error) {
	f, err := os.Open(filepath.Join(dir, workerSettingsName))
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxWorkerSettingsBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxWorkerSettingsBytes {
		return "", errors.New("ntvideo: worker settings exceed limit")
	}
	var settings workerSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return "", err
	}
	if settings.Version != 1 || settings.LibraryPath == "" || !filepath.IsAbs(settings.LibraryPath) {
		return "", errors.New("ntvideo: invalid worker settings")
	}
	return settings.LibraryPath, nil
}

// Preserve zero-copy staging where possible. Cross-volume Windows staging can
// require copying when both unprivileged symlinks and hard links fail.
func stageWorkerVideo(src, dst string, maxBytes int64, symlink, hardlink func(string, string) error) error {
	symlinkErr := symlink(src, dst)
	if symlinkErr == nil {
		return nil
	}
	linkErr := hardlink(src, dst)
	if linkErr == nil {
		return nil
	}
	if copyErr := copyWorkerFile(src, dst, maxBytes); copyErr != nil {
		return fmt.Errorf("ntvideo: stage video: %w", errors.Join(symlinkErr, linkErr, copyErr))
	}
	return nil
}
