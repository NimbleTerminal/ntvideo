// ntvideo-assets exports the browser host module to DIR/ntvideo.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"nimbleterminal.dev/ntvideo/web"
)

func run(args []string, out io.Writer) error {
	var dir string
	force := false
	for _, arg := range args {
		switch arg {
		case "--force", "-force":
			force = true
		default:
			if strings.HasPrefix(arg, "-") || dir != "" {
				return fmt.Errorf("usage: ntvideo-assets DIR [--force]")
			}
			dir = arg
		}
	}
	if dir == "" {
		return fmt.Errorf("usage: ntvideo-assets DIR [--force]")
	}
	return fs.WalkDir(web.Assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := web.Assets.ReadFile(name)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, "ntvideo", filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		f, err := os.OpenFile(dest, flags, 0644)
		if !force && os.IsExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		_, err = fmt.Fprintln(out, dest)
		return err
	})
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
