package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kapmcli/kapm/internal/agent"
	"github.com/kapmcli/kapm/internal/install"
	"github.com/kapmcli/kapm/internal/syncer"
)

var installRun = install.Run

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func expandTarget(s string) (string, error) {
	if s == "" {
		return ".", nil
	}
	if s == "~" || strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		rest, _ := strings.CutPrefix(s, "~")
		return filepath.Join(home, rest), nil
	}
	return s, nil
}

type command struct {
	name        string
	description string
	run         func(args []string) error
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	commands := map[string]command{
		"agent": {
			name:        "agent",
			description: "manage interactive .kiro agent configuration",
			run:         runAgent,
		},
		"install": {
			name:        "install",
			description: "install an APM package and sync it into .kiro/",
			run:         runInstall,
		},
		"sync": {
			name:        "sync",
			description: "sync local .apm content and apm_modules into .kiro/",
			run:         runSync,
		},
		"init-hook": {
			name:        "init-hook",
			description: "interactively add monitoring hooks to agents",
			run:         runInitHook,
		},
		"monitor": {
			name:        "monitor",
			description: "monitor agent activity metrics",
			run:         runMonitor,
		},
		"serve": {
			name:        "serve",
			description: "serve the kapm WebUI dashboard over HTTP",
			run:         runServe,
		},
		"version": {
			name:        "version",
			description: "print version information",
			run:         runVersion,
		},
	}

	if len(args) == 0 {
		printUsage(os.Stdout, commands)
		return nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(os.Stdout, commands)
		return nil
	}

	cmd, ok := commands[args[0]]
	if !ok {
		printUsage(os.Stderr, commands)
		return fmt.Errorf("unknown command %q", args[0])
	}

	return cmd.run(args[1:])
}

func runInstall(args []string) error {
	force, targetDirRaw, installArgs := splitInstallArgs(args)

	hasGlobal := slices.Contains(installArgs, "--global") || slices.Contains(installArgs, "-g")
	targetDirSet := targetDirRaw != "." && targetDirRaw != ""
	if hasGlobal && targetDirSet {
		return fmt.Errorf("--global と --target-dir は併用できません")
	}

	targetDir, err := expandTarget(targetDirRaw)
	if err != nil {
		return err
	}

	if hasGlobal {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		targetDir = home
	}

	return installRun(install.Options{
		InstallArgs: installArgs,
		Root:        targetDir,
		Force:       force,
	})
}

func splitInstallArgs(args []string) (force bool, targetDir string, installArgs []string) {
	installArgs = make([]string, 0, len(args))
	targetDir = "."

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--sync-force" {
			force = true
			continue
		}
		if arg == "--target-dir" || arg == "-target-dir" {
			if i+1 < len(args) {
				i++
				targetDir = args[i]
			}
			continue
		}
		if v, ok := strings.CutPrefix(arg, "--target-dir="); ok {
			targetDir = v
			continue
		}
		if v, ok := strings.CutPrefix(arg, "-target-dir="); ok {
			targetDir = v
			continue
		}
		installArgs = append(installArgs, arg)
	}

	return force, targetDir, installArgs
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite existing .kiro files")
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm sync [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Sync local .apm content and apm_modules into .kiro/.")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("sync does not accept positional arguments")
	}

	targetDir, err := expandTarget(*targetDirFlag)
	if err != nil {
		return err
	}

	return syncer.Run(syncer.Options{Root: targetDir, Force: *force})
}

func runAgent(args []string) error {
	if len(args) == 0 {
		printAgentUsage(os.Stdout)
		return nil
	}

	switch args[0] {
	case "generate":
		return runAgentGenerate(args[1:])
	case "update":
		return runAgentUpdate(args[1:])
	case "-h", "--help", "help":
		printAgentUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown agent subcommand %q", args[0])
	}
}

func runAgentGenerate(args []string) error {
	fs := flag.NewFlagSet("agent generate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite existing agent files")
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm agent generate [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Interactively create a new .kiro agent config.")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("agent generate does not accept positional arguments")
	}

	targetDir, err := expandTarget(*targetDirFlag)
	if err != nil {
		return err
	}

	return agent.Generate(agent.GenerateOptions{
		Root:  targetDir,
		Force: *force,
		In:    os.Stdin,
		Out:   os.Stdout,
	})
}

func runAgentUpdate(args []string) error {
	fs := flag.NewFlagSet("agent update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm agent update <name> [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Interactively update an existing .kiro agent config. Unknown fields are preserved.")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(fs.Args()) != 1 {
		return fmt.Errorf("agent update requires exactly one argument: <name>")
	}

	targetDir, err := expandTarget(*targetDirFlag)
	if err != nil {
		return err
	}

	return agent.Update(agent.UpdateOptions{
		Root: targetDir,
		Name: fs.Args()[0],
		In:   os.Stdin,
		Out:  os.Stdout,
	})
}

func printUsage(w io.Writer, commands map[string]command) {
	_, _ = fmt.Fprintln(w, "kapm adapts APM packages into Kiro-native configuration.")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  kapm <command> [arguments]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Available commands:")
	for _, name := range []string{"sync", "install", "agent", "init-hook", "monitor", "serve", "version"} {
		cmd := commands[name]
		_, _ = fmt.Fprintf(w, "  %-8s %s\n", cmd.name, cmd.description)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Use \"kapm <command> --help\" for more information about a command.")
}

func printAgentUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: kapm agent <subcommand>")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Available subcommands:")
	_, _ = fmt.Fprintln(w, "  generate  interactively create a new .kiro agent config")
	_, _ = fmt.Fprintln(w, "  update    interactively update an existing .kiro agent config")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Use \"kapm agent <subcommand> --help\" for more information about an agent subcommand.")
}

func runVersion(_ []string) error {
	fmt.Printf("kapm %s (commit: %s, built: %s)\n", version, commit, date)
	return nil
}

func runInitHook(args []string) error {
	fs := flag.NewFlagSet("init-hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	remove := fs.Bool("remove", false, "remove kapm hook entries instead of adding them")
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm init-hook [--remove]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Interactively add (or remove) kapm monitoring hooks to .kiro/agents/*.json.")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("init-hook does not accept positional arguments")
	}

	targetDir, err := expandTarget(*targetDirFlag)
	if err != nil {
		return err
	}

	return agent.InitHook(agent.InitHookOptions{
		Root:   targetDir,
		Remove: *remove,
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
	})
}
