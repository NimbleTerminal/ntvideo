//go:build (darwin || linux || windows) && (amd64 || arm64)

package ntvideo

import (
	"nimbleterminal.dev/ntmediabox"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMPVWorkerCustomLibrary(t *testing.T) {
	if os.Getenv("NTVIDEO_TEST_FILE") == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	dirs := append([]string{"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu"}, ntmediabox.DefaultLibPaths()...)
	var library string
	for _, name := range libraryNames("") {
		candidates := []string{name}
		if !filepath.IsAbs(name) {
			for _, dir := range dirs {
				candidates = append(candidates, filepath.Join(dir, name))
			}
		}
		for _, candidate := range candidates {
			if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
				library = candidate
				break
			}
		}
		if library != "" {
			break
		}
	}
	if library == "" {
		t.Skip("locate a libmpv file to test an explicit library path")
	}
	// A copy outside default library directories requires a literal read grant.
	copied := filepath.Join(t.TempDir(), filepath.Base(library))
	if err := copyWorkerFile(library, copied, 1<<30); err != nil {
		t.Fatal(err)
	}
	testMPVWorkerPlayback(t, copied)
}

func TestMPVWorkerInvalidCustomLibraryDoesNotFallback(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(t.TempDir(), "not-libmpv")
	if err := os.WriteFile(library, []byte("invalid shared library"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := newWorkerBackend(config{libraryPath: library, worker: WorkerConfig{WorkerPath: exe, WorkerArgs: []string{testWorkerArg}, StagePath: testSandboxStage(t), AllowUnsandboxed: runtime.GOOS == "windows"}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Load(Source{URL: source}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := b.Poll()
		if status.State == Failed && err != nil {
			return
		}
		if status.State == Paused || status.State == Playing {
			t.Fatalf("ignored explicit invalid library: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not report custom library load failure")
}
