package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/kazuhideoki/veil/internal/infra"
	"github.com/kazuhideoki/veil/internal/usecase"
)

func main() {
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usecase.HelpText())
	}

	flag.Parse()

	if err := run(flag.Args(), os.Stdout, os.Stderr); err != nil {
		writeError(os.Stderr, err)
		os.Exit(1)
	}
}

func writeError(w io.Writer, err error) {
	fmt.Fprintf(w, "\x1b[31m%v\x1b[0m\n", err)
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

		return withStateLock(runner.Run)
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
			FileSystem:      infra.OSFileSystem{},
			TrackedChecker:  infra.GitCLI{},
			DocumentRuntime: infra.OnePasswordDocumentRuntime{},
			Stdout:          stdout,
			TargetPath:      addFlags.Arg(0),
		}

		return withStateLock(runner.Run)
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
			FileSystem:      infra.OSFileSystem{},
			DocumentRuntime: infra.OnePasswordDocumentRuntime{},
			EditorRunner:    infra.ExecEditorRunner{},
			EditorPath:      os.Getenv("EDITOR"),
			TargetPath:      editFlags.Arg(0),
		}

		return withStateLock(runner.Run)
	case "update":
		updateFlags := flag.NewFlagSet("update", flag.ContinueOnError)
		updateFlags.SetOutput(stderr)

		if err := updateFlags.Parse(args[1:]); err != nil {
			return err
		}

		if updateFlags.NArg() != 1 {
			return fmt.Errorf("update requires exactly one target path")
		}

		runner := usecase.UpdateTarget{
			FileSystem:      infra.OSFileSystem{},
			DocumentRuntime: infra.OnePasswordDocumentRuntime{},
			Stdout:          stdout,
			TargetPath:      updateFlags.Arg(0),
		}

		return withStateLock(runner.Run)
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

		return withStateLock(runner.Run)
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

		return withStateLock(runner.Run)
	case "workspace":
		if len(args) < 2 {
			return fmt.Errorf("workspace requires a subcommand")
		}

		switch args[1] {
		case "remove":
			workspaceRemoveFlags := flag.NewFlagSet("workspace remove", flag.ContinueOnError)
			workspaceRemoveFlags.SetOutput(stderr)

			var workspaceID string
			workspaceRemoveFlags.StringVar(&workspaceID, "workspace-id", "", "registered workspace id to remove")

			if err := workspaceRemoveFlags.Parse(args[2:]); err != nil {
				return err
			}

			if workspaceRemoveFlags.NArg() != 0 {
				return fmt.Errorf("workspace remove does not accept positional arguments: %v", workspaceRemoveFlags.Args())
			}

			runner := usecase.RemoveWorkspace{
				FileSystem:  infra.OSFileSystem{},
				Stdout:      stdout,
				WorkspaceID: workspaceID,
			}

			return withStateLock(runner.Run)
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

			return withStateLock(runner.Run)
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

		cleaner := usecase.RunTTLCleaner{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
		}
		runner := usecase.EmergeTargets{
			FileSystem:      infra.OSFileSystem{},
			DocumentRuntime: infra.OnePasswordDocumentRuntime{},
			Stdout:          stdout,
			AllWorkspaces:   allWorkspaces,
		}
		agent, err := defaultTTLAgent(stdout)
		if err != nil {
			return err
		}

		return withStateLock(func() error {
			if err := agent.EnsureInstalled(); err != nil {
				return err
			}
			if err := cleaner.Run(); err != nil {
				return err
			}
			return runner.Run()
		})
	case "vanish":
		vanishFlags := flag.NewFlagSet("vanish", flag.ContinueOnError)
		vanishFlags.SetOutput(stderr)

		var allWorkspaces bool
		var commit bool
		var discard bool
		vanishFlags.BoolVar(&allWorkspaces, "all", false, "vanish registered targets for all workspaces")
		vanishFlags.BoolVar(&commit, "commit", false, "commit modified 1Password document targets before vanishing")
		vanishFlags.BoolVar(&discard, "discard", false, "discard modified 1Password document targets while vanishing")

		if err := vanishFlags.Parse(args[1:]); err != nil {
			return err
		}

		if vanishFlags.NArg() != 0 {
			return fmt.Errorf("vanish does not accept positional arguments: %v", vanishFlags.Args())
		}

		runner := usecase.VanishTargets{
			FileSystem:      infra.OSFileSystem{},
			DocumentRuntime: infra.OnePasswordDocumentRuntime{},
			Stdout:          stdout,
			AllWorkspaces:   allWorkspaces,
			Commit:          commit,
			Discard:         discard,
		}

		return withStateLock(runner.Run)
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

		runner := usecase.RunTTLCleaner{
			FileSystem: infra.OSFileSystem{},
			Stdout:     stdout,
		}

		return withStateLock(runner.Run)
	case "ttl-agent":
		if len(args) < 2 {
			return fmt.Errorf("ttl-agent requires a subcommand")
		}

		switch args[1] {
		case "install":
			agentFlags := flag.NewFlagSet("ttl-agent install", flag.ContinueOnError)
			agentFlags.SetOutput(stderr)

			var intervalSeconds int
			var label string
			agentFlags.IntVar(&intervalSeconds, "interval", 60, "seconds between ttl-cleaner runs")
			agentFlags.StringVar(&label, "label", usecase.DefaultTTLAgentLabel, "launch agent label")

			if err := agentFlags.Parse(args[2:]); err != nil {
				return err
			}
			if agentFlags.NArg() != 0 {
				return fmt.Errorf("ttl-agent install does not accept positional arguments: %v", agentFlags.Args())
			}

			runner, err := defaultTTLAgent(stdout)
			if err != nil {
				return err
			}
			runner.Label = label
			runner.IntervalSeconds = intervalSeconds
			return runner.Install()
		case "uninstall":
			agentFlags := flag.NewFlagSet("ttl-agent uninstall", flag.ContinueOnError)
			agentFlags.SetOutput(stderr)

			var label string
			agentFlags.StringVar(&label, "label", usecase.DefaultTTLAgentLabel, "launch agent label")

			if err := agentFlags.Parse(args[2:]); err != nil {
				return err
			}
			if agentFlags.NArg() != 0 {
				return fmt.Errorf("ttl-agent uninstall does not accept positional arguments: %v", agentFlags.Args())
			}

			runner := usecase.TTLAgent{
				FileSystem:    infra.OSFileSystem{},
				CommandRunner: infra.ExecCommandRunner{},
				Stdout:        stdout,
				UserID:        strconv.Itoa(os.Getuid()),
				Label:         label,
			}
			return runner.Uninstall()
		case "status":
			agentFlags := flag.NewFlagSet("ttl-agent status", flag.ContinueOnError)
			agentFlags.SetOutput(stderr)

			var label string
			agentFlags.StringVar(&label, "label", usecase.DefaultTTLAgentLabel, "launch agent label")

			if err := agentFlags.Parse(args[2:]); err != nil {
				return err
			}
			if agentFlags.NArg() != 0 {
				return fmt.Errorf("ttl-agent status does not accept positional arguments: %v", agentFlags.Args())
			}

			runner := usecase.TTLAgent{
				FileSystem:    infra.OSFileSystem{},
				CommandRunner: infra.ExecCommandRunner{},
				Stdout:        stdout,
				UserID:        strconv.Itoa(os.Getuid()),
				Label:         label,
			}
			return runner.Status()
		default:
			return fmt.Errorf("unsupported ttl-agent arguments: %v", args[1:])
		}
	default:
		return fmt.Errorf("unsupported arguments: %v", args)
	}
}

func defaultTTLAgent(stdout io.Writer) (usecase.TTLAgent, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return usecase.TTLAgent{}, fmt.Errorf("resolve executable path: %w", err)
	}
	return usecase.TTLAgent{
		FileSystem:      infra.OSFileSystem{},
		CommandRunner:   infra.ExecCommandRunner{},
		Stdout:          stdout,
		ExecutablePath:  executablePath,
		UserID:          strconv.Itoa(os.Getuid()),
		IntervalSeconds: 60,
	}, nil
}

func withStateLock(run func() error) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	lockDir := filepath.Join(homeDir, ".veil")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("create state lock directory: %w", err)
	}
	lockFile, err := os.OpenFile(filepath.Join(lockDir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open state lock: %w", err)
	}
	defer func() {
		_ = lockFile.Close()
	}()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	return run()
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
