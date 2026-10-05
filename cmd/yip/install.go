package main

import (
	"errors"
	"flag"
	"os"

	"github.com/binbandit/yip/internal/install"
)

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	binDir := fs.String("bin-dir", "", "install directory (default: existing yip on PATH, otherwise ~/.local/bin)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: yip install [--bin-dir DIR]")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	return install.Install(source, *binDir, os.Stdout)
}
