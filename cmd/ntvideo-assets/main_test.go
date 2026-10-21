package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
