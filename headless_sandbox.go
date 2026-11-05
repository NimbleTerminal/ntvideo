package ntvideo

import (
	"context"
	"os/exec"
	"path/filepath"

	"nimbleterminal.dev/ntmediabox"
)

func (o HeadlessOptions) runTool(ctx context.Context, path, name string, maxOutput int, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o.Sandbox == nil {
		return runMediaTool(ctx, name, maxOutput, args...)
	}
	tool, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	tool, err = filepath.Abs(tool)
	if err != nil {
		return nil, err
	}
	tool, err = ntmediabox.VerifyBinary(tool)
	if err != nil {
		return nil, err
	}
	policy := ntmediabox.DefaultPolicy(tool, path)
	policy.AllowUnsandboxed = o.Sandbox.AllowUnsandboxed
	cmd, _, err := ntmediabox.Command(ntmediabox.HelperConfig{
		Policy: policy, WorkerPath: tool, WorkerArgs: args, StagePath: o.Sandbox.StagePath,
	})
	if err != nil {
		return nil, err
	}
	// collectMediaTool starts cmd itself. Install cancellation through exec.Cmd's
	// context support by constructing a CommandContext with the confined argv.
	confined := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	confined.Env = cmd.Env
	confined.SysProcAttr = cmd.SysProcAttr
	confined.ExtraFiles = cmd.ExtraFiles
	confined.Cancel = func() error {
		ntmediabox.KillHelper(confined.Process.Pid)
		return nil
	}
	// The command API owns no streaming connection; close inherited stage files
	// after Run, including failures before the subprocess starts.
	defer func() {
		for _, file := range confined.ExtraFiles {
			_ = file.Close()
		}
	}()
	return collectMediaTool(ctx, name, confined, maxOutput)
}
