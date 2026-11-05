package ntvideo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntmediabox"
)

const testHeadlessArg = "--ntvideo-test-headless"

func runHeadlessTestTool(args []string) {
	switch args[0] {
	case "read":
		data, err := os.ReadFile(args[1])
		if err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.Write(data)
	case "hang":
		time.Sleep(time.Minute)
	case "overflow":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 4096))
	}
}

func testSandboxStage(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		return ""
	}
	path := filepath.Join(t.TempDir(), ntmediabox.DefaultStageBinary)
	cmd := exec.Command("go", "build", "-o", path, "nimbleterminal.dev/ntconfine/cmd/ntconfine-stage")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ntconfine-stage: %v: %s", err, out)
	}
	return path
}

func TestHeadlessSandboxPolicyAndCancellation(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native executable sandbox")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "input.mp4")
	secret := filepath.Join(t.TempDir(), "secret.txt")
	for _, path := range []string{input, secret} {
		if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	input, err = validateLocalFile(input, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	opts := HeadlessOptions{Sandbox: &HeadlessSandbox{}}
	if !ntmediabox.Available() {
		_, err := opts.runTool(context.Background(), input, exe, 32, testHeadlessArg, "read", input)
		if !errors.Is(err, ntmediabox.ErrUnavailable) {
			t.Fatalf("sandbox did not fail closed: %v", err)
		}
		return
	}
	opts.Sandbox.StagePath = testSandboxStage(t)
	data, err := opts.runTool(context.Background(), input, exe, 32, testHeadlessArg, "read", input)
	if err != nil || string(data) != "private" {
		t.Fatalf("allowed input: %q %v", data, err)
	}
	if _, err := opts.runTool(context.Background(), input, exe, 32, testHeadlessArg, "read", secret); err == nil {
		t.Fatal("sandbox read unrelated file")
	}
	if _, err := opts.runTool(context.Background(), input, exe, 32, testHeadlessArg, "overflow"); err == nil {
		t.Fatal("output limit ignored")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := opts.runTool(ctx, input, exe, 32, testHeadlessArg, "hang"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("sandbox cancellation did not terminate promptly")
	}
}

func TestHeadlessMediaSandbox(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	if !ntmediabox.Available() {
		t.Skip("OS sandbox unavailable")
	}
	opts := HeadlessOptions{Sandbox: &HeadlessSandbox{StagePath: testSandboxStage(t)}}
	info, err := Probe(context.Background(), source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		t.Fatalf("invalid metadata: %+v", info)
	}
	frames, err := Frames(context.Background(), source, []time.Duration{time.Second, 0, time.Second}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[0] == frames[2] {
		t.Fatal("frame order or ownership lost")
	}
	if _, err := FrameAt(context.Background(), source, 0, opts); err != nil {
		t.Fatal(err)
	}
	// Container transcript extraction must use the same confinement policy for
	// both its probe and FFmpeg conversion subprocesses.
	container := makeSubtitleContainer(t, source)
	cues, err := Cues(context.Background(), container, opts)
	if err != nil || len(cues) == 0 {
		t.Fatalf("confined transcript: %v %v", cues, err)
	}
}

func TestHeadlessToolOutputLimit(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("native subprocess")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := runMediaTool(ctx, exe, 32, testHeadlessArg, "overflow"); err == nil {
		t.Fatal("direct subprocess bypassed output limit")
	}
}
