package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kazuhideoki/veil/internal/infra"
	"github.com/kazuhideoki/veil/internal/usecase"
)

func main() {
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usecase.HelpText())
	}

	flag.Parse()

	if err := run(flag.Args(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return nil
	}

	switch args[0] {
	case "init":
		if len(args) != 1 {
			return fmt.Errorf("init does not accept positional arguments: %v", args[1:])
		}

		runner := usecase.InitConfig{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
		}

		return runner.Run()
	default:
		return fmt.Errorf("unsupported arguments: %v", args)
	}
}
