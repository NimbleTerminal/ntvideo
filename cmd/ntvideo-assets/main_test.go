package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedAssetWriteDoesNotPublish(t *testing.T) {
	for _, force := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "surface.js")
		if force {
			if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		failure := errors.New("write failed")
		written, err := publishAsset(path, force, func(w io.Writer) error {
			if _, err := io.WriteString(w, "partial"); err != nil {
				return err
			}
			return failure
		})
		if written || !errors.Is(err, failure) {
			t.Fatalf("published failed write: %v, %v", written, err)
		}
		data, err := os.ReadFile(path)
		if force && (err != nil || string(data) != "original") || !force && !os.IsNotExist(err) {
			t.Fatalf("destination changed: %q, %v", data, err)
		}
		entries, err := os.ReadDir(dir)
		want := 0
		if force {
			want = 1
		}
		if err != nil || len(entries) != want {
			t.Fatalf("temporary file leaked: %v, %v", entries, err)
		}
	}
}

func TestExportAndOverwritePolicy(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{dir}, &out); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ntvideo", "surface.js")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(original), "registerHost") || !strings.Contains(out.String(), path) {
		t.Fatal("asset or written-file report missing")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("unexpected published assets", entries, err)
	}
	if err = os.WriteFile(path, []byte("host customization"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{dir}, &out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "host customization" {
		t.Fatal("existing asset overwritten")
	}
	if err = run([]string{dir, "--force"}, &out); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("force did not refresh asset")
	}
}
func TestExportErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"--bad"}, {"a", "b"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("file"), 0600)
	if err := run([]string{path}, &bytes.Buffer{}); err == nil {
		t.Fatal("destination error ignored")
	}
}

func TestAssetPublicationPreservesConcurrentDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "surface.js")
	written, err := publishAsset(path, false, func(w io.Writer) error {
		if _, err := io.WriteString(w, "new asset"); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("other exporter"), 0644)
	})
	if err != nil || written {
		t.Fatalf("concurrent destination was replaced: %v, %v", written, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "other exporter" {
		t.Fatalf("concurrent destination changed: %q, %v", data, err)
	}
}
