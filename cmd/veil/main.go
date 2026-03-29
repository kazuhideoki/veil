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
		initFlags := flag.NewFlagSet("init", flag.ContinueOnError)
		initFlags.SetOutput(stderr)

		var workspaceID string
		initFlags.StringVar(&workspaceID, "workspace-id", "", "workspace id to add")

		if err := initFlags.Parse(args[1:]); err != nil {
			return err
		}

		if initFlags.NArg() != 0 {
			return fmt.Errorf("init does not accept positional arguments: %v", initFlags.Args())
		}

		runner := usecase.InitConfig{
			FileSystem:  infra.OSFileSystem{},
			Stdout:      stdout,
			WorkspaceID: workspaceID,
		}

		return runner.Run()
	case "add":
		addFlags := flag.NewFlagSet("add", flag.ContinueOnError)
		addFlags.SetOutput(stderr)

		if err := addFlags.Parse(args[1:]); err != nil {
			return err
		}

		if addFlags.NArg() != 1 {
			return fmt.Errorf("add requires exactly one target path")
		}

		runner := usecase.AddTarget{
			FileSystem:     infra.OSFileSystem{},
			TrackedChecker: infra.GitCLI{},
			Stdout:         stdout,
			TargetPath:     addFlags.Arg(0),
		}

		return runner.Run()
	case "edit":
		editFlags := flag.NewFlagSet("edit", flag.ContinueOnError)
		editFlags.SetOutput(stderr)

		if err := editFlags.Parse(args[1:]); err != nil {
			return err
		}

		if editFlags.NArg() != 1 {
			return fmt.Errorf("edit requires exactly one target path")
		}

		runner := usecase.EditTarget{
			FileSystem:   infra.OSFileSystem{},
			EditorRunner: infra.ExecEditorRunner{},
			EditorPath:   os.Getenv("EDITOR"),
			TargetPath:   editFlags.Arg(0),
		}

		return runner.Run()
	case "emerge":
		emergeFlags := flag.NewFlagSet("emerge", flag.ContinueOnError)
		emergeFlags.SetOutput(stderr)

		if err := emergeFlags.Parse(args[1:]); err != nil {
			return err
		}

		if emergeFlags.NArg() != 0 {
			return fmt.Errorf("emerge does not accept positional arguments: %v", emergeFlags.Args())
		}

		runner := usecase.EmergeTargets{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
		}

		return runner.Run()
	case "vanish":
		vanishFlags := flag.NewFlagSet("vanish", flag.ContinueOnError)
		vanishFlags.SetOutput(stderr)

		if err := vanishFlags.Parse(args[1:]); err != nil {
			return err
		}

		if vanishFlags.NArg() != 0 {
			return fmt.Errorf("vanish does not accept positional arguments: %v", vanishFlags.Args())
		}

		runner := usecase.VanishTargets{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
		}

		return runner.Run()
	default:
		return fmt.Errorf("unsupported arguments: %v", args)
	}
}
