package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	case "remove":
		removeFlags := flag.NewFlagSet("remove", flag.ContinueOnError)
		removeFlags.SetOutput(stderr)

		if err := removeFlags.Parse(args[1:]); err != nil {
			return err
		}

		if removeFlags.NArg() != 1 {
			return fmt.Errorf("remove requires exactly one target path")
		}

		runner := usecase.RemoveTarget{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
			TargetPath: removeFlags.Arg(0),
		}

		return runner.Run()
	case "purge":
		purgeFlags := flag.NewFlagSet("purge", flag.ContinueOnError)
		purgeFlags.SetOutput(stderr)

		if err := purgeFlags.Parse(args[1:]); err != nil {
			return err
		}

		if purgeFlags.NArg() != 1 {
			return fmt.Errorf("purge requires exactly one target path")
		}

		runner := usecase.PurgeTarget{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
			TargetPath: purgeFlags.Arg(0),
		}

		return runner.Run()
	case "workspace":
		if len(args) < 2 {
			return fmt.Errorf("workspace requires a subcommand")
		}

		switch args[1] {
		case "remove":
			workspaceRemoveFlags := flag.NewFlagSet("workspace remove", flag.ContinueOnError)
			workspaceRemoveFlags.SetOutput(stderr)

			if err := workspaceRemoveFlags.Parse(args[2:]); err != nil {
				return err
			}

			if workspaceRemoveFlags.NArg() != 0 {
				return fmt.Errorf("workspace remove does not accept positional arguments: %v", workspaceRemoveFlags.Args())
			}

			runner := usecase.RemoveWorkspace{
				FileSystem: infra.OSFileSystem{},
				Stdout:     stdout,
			}

			return runner.Run()
		case "purge":
			workspacePurgeFlags := flag.NewFlagSet("workspace purge", flag.ContinueOnError)
			workspacePurgeFlags.SetOutput(stderr)

			var assumeYes bool
			workspacePurgeFlags.BoolVar(&assumeYes, "yes", false, "skip confirmation")

			if err := workspacePurgeFlags.Parse(args[2:]); err != nil {
				return err
			}

			if workspacePurgeFlags.NArg() != 0 {
				return fmt.Errorf("workspace purge does not accept positional arguments: %v", workspacePurgeFlags.Args())
			}

			runner := usecase.PurgeWorkspace{
				FileSystem:  infra.OSFileSystem{},
				Stdin:       os.Stdin,
				Stdout:      stdout,
				Interactive: stdinIsTerminal(),
				AssumeYes:   assumeYes,
			}

			return runner.Run()
		default:
			return fmt.Errorf("unsupported workspace arguments: %v", args[1:])
		}
	case "emerge":
		emergeFlags := flag.NewFlagSet("emerge", flag.ContinueOnError)
		emergeFlags.SetOutput(stderr)

		var allWorkspaces bool
		emergeFlags.BoolVar(&allWorkspaces, "all", false, "emerge registered targets for all workspaces")

		if err := emergeFlags.Parse(args[1:]); err != nil {
			return err
		}

		if emergeFlags.NArg() != 0 {
			return fmt.Errorf("emerge does not accept positional arguments: %v", emergeFlags.Args())
		}

		runner := usecase.EmergeTargets{
			FileSystem:     infra.OSFileSystem{},
			Stdout:         stdout,
			CleanerStarter: infra.ExecTTLCleanerStarter{},
			AllWorkspaces:  allWorkspaces,
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
	case "status":
		statusFlags := flag.NewFlagSet("status", flag.ContinueOnError)
		statusFlags.SetOutput(stderr)

		if err := statusFlags.Parse(args[1:]); err != nil {
			return err
		}

		if statusFlags.NArg() != 0 {
			return fmt.Errorf("status does not accept positional arguments: %v", statusFlags.Args())
		}

		runner := usecase.StatusTargets{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
		}

		return runner.Run()
	case "ttl-cleaner":
		cleanerFlags := flag.NewFlagSet("ttl-cleaner", flag.ContinueOnError)
		cleanerFlags.SetOutput(stderr)

		if err := cleanerFlags.Parse(args[1:]); err != nil {
			return err
		}

		if cleanerFlags.NArg() != 0 {
			return fmt.Errorf("ttl-cleaner does not accept positional arguments: %v", cleanerFlags.Args())
		}

		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory: %w", err)
		}

		runner := usecase.RunTTLCleaner{
			FileSystem: infra.OSFileSystem{},
			Lock: &infra.FileTTLCleanerLock{
				Path: filepath.Join(homeDir, ".veil", "ttl-cleaner.lock"),
			},
			Stdout: stdout,
		}

		return runner.Run()
	default:
		return fmt.Errorf("unsupported arguments: %v", args)
	}
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
