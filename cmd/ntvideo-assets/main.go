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
		written, err := publishAsset(dest, force, func(w io.Writer) error {
			_, err := w.Write(data)
			return err
		})
		if err != nil || !written {
			return err
		}
		_, err = fmt.Fprintln(out, dest)
		return err
	})
}

// Stage a complete asset beside its destination. Link publishes without
// replacing an existing file; Rename implements explicit --force replacement.
func publishAsset(dest string, force bool, write func(io.Writer) error) (bool, error) {
	if !force {
		if _, err := os.Lstat(dest); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".ntvideo-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	writeErr := write(f)
	if writeErr == nil {
		writeErr = f.Chmod(0644)
	}
	closeErr := f.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if force {
		err = os.Rename(f.Name(), dest)
	} else {
		err = os.Link(f.Name(), dest)
		if os.IsExist(err) {
			return false, nil
		}
	}
	return err == nil, err
}
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
