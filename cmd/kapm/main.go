package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kapmcli/kapm/internal/agent"
	"github.com/kapmcli/kapm/internal/hook"
	"github.com/kapmcli/kapm/internal/hookdump"
	"github.com/kapmcli/kapm/internal/idehook"
	"github.com/kapmcli/kapm/internal/install"
	"github.com/kapmcli/kapm/internal/power"
	"github.com/kapmcli/kapm/internal/syncer"
)

var installRun = install.Run
var powerInstallRun = power.Install

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
		os.Exit(exitCodeOf(err))
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
		"init-ide-hook": {
			name:        "init-ide-hook",
			description: "add monitoring hooks to Kiro IDE",
			run:         runInitIDEHook,
		},
		"power": {
			name:        "power",
			description: "install Kiro Power packages into .kiro/powers/",
			run:         runPower,
		},
		"hook-handler": {
			name:        "hook-handler",
			description: "handle a Kiro hook event and append logs",
			run:         runHookHandler,
		},
		"hook-dump": {
			name:        "hook-dump",
			description: "dump raw hook input for debugging",
			run:         runHookDump,
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

func parseFlagSet(fs *flag.FlagSet, args []string) (bool, error) {
	originalOutput := fs.Output()
	if hasHelpFlag(args) {
		fs.SetOutput(os.Stdout)
		defer fs.SetOutput(originalOutput)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type usageError struct {
	msg string
}

func (e usageError) Error() string {
	return e.msg
}

func exitCodeOf(err error) int {
	if _, ok := errors.AsType[usageError](err); ok {
		return 2
	}
	return 1
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "-help":
			return true
		}
	}
	return false
}

func rejectPositionalArgs(fs *flag.FlagSet, command string) error {
	if len(fs.Args()) > 0 {
		return fmt.Errorf("%s does not accept positional arguments", command)
	}
	return nil
}

func runInstall(args []string) error {
	force, targetDirRaw, installArgs := splitInstallArgs(args)

	hasGlobal := slices.Contains(installArgs, "--global") || slices.Contains(installArgs, "-g")
	targetDirSet := targetDirRaw != "." && targetDirRaw != ""
	if hasGlobal && targetDirSet {
		return errors.New("--global and --target-dir cannot be used together")
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
		if arg == "--target-dir" {
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

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "sync"); err != nil {
		return err
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

func runPower(args []string) error {
	if len(args) == 0 {
		printPowerUsage(os.Stdout)
		return nil
	}

	switch args[0] {
	case "install":
		return runPowerInstall(args[1:])
	case "-h", "--help", "help":
		printPowerUsage(os.Stdout)
		return nil
	default:
		return usageError{msg: fmt.Sprintf("unknown power subcommand %q", args[0])}
	}
}

func runPowerInstall(args []string) error {
	fs := flag.NewFlagSet("power install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite existing kapm-managed power output")
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	refFlag := fs.String("ref", "", "override git ref for git sources")
	timeoutFlag := fs.Duration("timeout", power.DefaultTimeout, "git fetch timeout")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm power install <url-or-path> [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Install a Kiro Power package into .kiro/powers/ and suggest raw POWER/steering resources.")
		fs.PrintDefaults()
	}

	ok, err := parseFlagSet(fs, normalizePowerInstallArgs(args))
	if err != nil {
		return usageError{msg: err.Error()}
	}
	if !ok {
		return nil
	}

	installOpts, err := powerInstallOptionsFromFlagSet(fs, *targetDirFlag, *refFlag, *force, *timeoutFlag)
	if err != nil {
		return err
	}

	result, err := powerInstallRun(context.Background(), installOpts)
	if err != nil {
		return err
	}
	printPowerInstallResult(os.Stdout, result)
	return nil
}

func powerInstallOptionsFromFlagSet(
	fs *flag.FlagSet,
	targetDirFlag string,
	refFlag string,
	force bool,
	timeout time.Duration,
) (power.InstallOptions, error) {
	if len(fs.Args()) == 0 {
		return power.InstallOptions{}, usageError{msg: "power install requires exactly one argument: <url-or-path>"}
	}
	if len(fs.Args()) != 1 {
		return power.InstallOptions{}, usageError{msg: "power install requires exactly one argument: <url-or-path>"}
	}

	targetDir, err := expandTarget(targetDirFlag)
	if err != nil {
		return power.InstallOptions{}, err
	}

	source, err := power.ParsePowerSource(fs.Args()[0])
	if err != nil {
		return power.InstallOptions{}, err
	}
	if refFlag != "" && source.Kind != power.SourceLocal {
		source.Ref = refFlag
	}

	return power.InstallOptions{
		Source:    source,
		TargetDir: targetDir,
		Force:     force,
		Timeout:   timeout,
	}, nil
}

func printPowerInstallResult(w io.Writer, result *power.Result) {
	_, _ = fmt.Fprintf(w, "Installed power %q to %s\n", result.Name, result.PowerDir)
	if result.ResolvedCommit != "" {
		_, _ = fmt.Fprintf(w, "Resolved commit: %s\n", result.ResolvedCommit)
	}
	printPowerInstallSuggestions(w, result)
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}

func printPowerInstallSuggestions(w io.Writer, result *power.Result) {
	_, _ = fmt.Fprintln(w, "Suggested custom agent config:")
	_, _ = fmt.Fprintln(w, `"resources": [`)
	for i, resourcePath := range result.ResourcePaths {
		suffix := ","
		if i == len(result.ResourcePaths)-1 {
			suffix = ""
		}
		_, _ = fmt.Fprintf(w, "  \"file://%s\"%s\n", filepath.ToSlash(resourcePath), suffix)
	}
	_, _ = fmt.Fprintln(w, `]`)

	if result.MCPConfigPath != "" {
		mcpServers, err := readMCPServers(result.MCPConfigPath)
		if err != nil {
			_, _ = fmt.Fprintf(w, "MCP config: copy %s into the agent's mcpServers field (could not render snippet: %v)\n", result.MCPConfigPath, err)
		} else {
			_, _ = fmt.Fprintln(w, `"mcpServers":`)
			_, _ = w.Write(mcpServers)
			if len(mcpServers) == 0 || mcpServers[len(mcpServers)-1] != '\n' {
				_, _ = fmt.Fprintln(w)
			}
		}
	}

	if result.HooksDir != "" {
		hookFiles, err := listPowerHookFiles(result.HooksDir)
		if err != nil {
			_, _ = fmt.Fprintf(w, "Hooks: adapt files under %s into the agent's hooks field (could not list files: %v)\n", result.HooksDir, err)
		} else {
			_, _ = fmt.Fprintln(w, `Hook files to adapt into the agent's "hooks" field:`)
			for _, hookFile := range hookFiles {
				_, _ = fmt.Fprintf(w, "- %s\n", hookFile)
			}
		}
	}

	_, _ = fmt.Fprintf(w, "Remove: rm -rf %s\n", result.PowerDir)
}

func readMCPServers(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return json.MarshalIndent(config.MCPServers, "", "  ")
}

func listPowerHookFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}

func normalizePowerInstallArgs(args []string) []string {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--force":
			flagArgs = append(flagArgs, arg)
		case arg == "--target-dir" || arg == "--ref" || arg == "--timeout":
			flagArgs = append(flagArgs, arg)
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		case strings.HasPrefix(arg, "--target-dir=") || strings.HasPrefix(arg, "--ref=") || strings.HasPrefix(arg, "--timeout="):
			flagArgs = append(flagArgs, arg)
		case strings.HasPrefix(arg, "-"):
			flagArgs = append(flagArgs, arg)
		default:
			positionals = append(positionals, arg)
		}
	}

	return append(flagArgs, positionals...)
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

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "agent generate"); err != nil {
		return err
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

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if len(fs.Args()) != 1 {
		return errors.New("agent update requires exactly one argument: <name>")
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
	for _, name := range []string{"sync", "install", "power", "agent", "init-hook", "init-ide-hook", "hook-handler", "hook-dump", "monitor", "serve", "version"} {
		cmd := commands[name]
		_, _ = fmt.Fprintf(w, "  %-13s %s\n", cmd.name, cmd.description)
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

func printPowerUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: kapm power <subcommand>")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Available subcommands:")
	_, _ = fmt.Fprintln(w, "  install   install a Kiro Power package into .kiro/powers/")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Use \"kapm power <subcommand> --help\" for more information about a power subcommand.")
}

func runVersion(_ []string) error {
	fmt.Printf("kapm %s (commit: %s, built: %s)\n", version, commit, date)
	return nil
}

func runInitHook(args []string) error {
	fs := flag.NewFlagSet("init-hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	remove := fs.Bool("remove", false, "remove kapm hook entries instead of adding them")
	global := fs.Bool("global", false, "use the home directory as the target directory")
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm init-hook [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Interactively add (or remove) kapm monitoring hooks to .kiro/agents/*.json.")
		fs.PrintDefaults()
	}

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "init-hook"); err != nil {
		return err
	}

	targetDirSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "target-dir" {
			targetDirSet = true
		}
	})
	if *global && targetDirSet {
		return errors.New("--global and --target-dir cannot be used together")
	}

	targetDir, err := expandTarget(*targetDirFlag)
	if err != nil {
		return err
	}
	if *global {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		targetDir = home
	}

	return agent.InitHook(agent.InitHookOptions{
		Root:   targetDir,
		Remove: *remove,
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
	})
}

func runInitIDEHook(args []string) error {
	fs := flag.NewFlagSet("init-ide-hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	remove := fs.Bool("remove", false, "remove kapm IDE hook files instead of adding them")
	targetDirFlag := fs.String("target-dir", ".", "target directory (default: current directory)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm init-ide-hook [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Add (or remove) kapm monitoring hook files under .kiro/hooks/ for Kiro IDE.")
		fs.PrintDefaults()
	}

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "init-ide-hook"); err != nil {
		return err
	}

	targetDir, err := expandTarget(*targetDirFlag)
	if err != nil {
		return err
	}

	return idehook.Init(idehook.Options{
		Root:   targetDir,
		Remove: *remove,
		Out:    os.Stdout,
	})
}

func runHookHandler(args []string) error {
	fs := flag.NewFlagSet("hook-handler", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentName := fs.String("agent", "", "agent name to attach to hook log records")
	eventName := fs.String("event", "", "fallback hook event name when stdin is empty")
	sessionID := fs.String("session", "", "fallback session id when stdin is empty")
	toolName := fs.String("tool", "", "fallback tool name when stdin is empty")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm hook-handler [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Handle a Kiro hook event from stdin and append a JSONL log record.")
		fs.PrintDefaults()
	}

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "hook-handler"); err != nil {
		return err
	}

	input, resolvedAgent, err := prepareHookHandlerInput(hookHandlerInputOptions{
		Stdin:     os.Stdin,
		Agent:     *agentName,
		Event:     *eventName,
		SessionID: *sessionID,
		Tool:      *toolName,
		Getenv:    os.Getenv,
	})
	if err != nil {
		return err
	}
	hook.Handle(input, os.Stdout, os.Stderr, time.Now, ".", resolvedAgent)
	return nil
}

type hookHandlerInputOptions struct {
	Stdin     io.Reader
	Agent     string
	Event     string
	SessionID string
	Tool      string
	Getenv    func(string) string
}

func prepareHookHandlerInput(opts hookHandlerInputOptions) (io.Reader, string, error) {
	agentName := opts.Agent
	if agentName == "" && opts.Getenv != nil {
		agentName = opts.Getenv("AGENT")
	}
	if opts.Event == "" {
		return opts.Stdin, agentName, nil
	}

	data, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, "", err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		fallback := map[string]string{
			"hook_event_name": opts.Event,
			"session_id":      opts.SessionID,
			"tool_name":       opts.Tool,
		}
		data, err = json.Marshal(fallback)
		if err != nil {
			return nil, "", err
		}
	}
	return bytes.NewReader(data), agentName, nil
}

func runHookDump(args []string) error {
	fs := flag.NewFlagSet("hook-dump", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentName := fs.String("agent", "", "agent name to attach to hook input dump records")
	eventName := fs.String("event", "", "hook event name to attach to hook input dump records")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: kapm hook-dump [flags]\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Dump raw hook stdin and selected Kiro environment variables to .kapm/logs/hook-input.jsonl.")
		fs.PrintDefaults()
	}

	ok, err := parseFlagSet(fs, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := rejectPositionalArgs(fs, "hook-dump"); err != nil {
		return err
	}
	if *agentName == "" {
		*agentName = os.Getenv("AGENT")
	}
	return hookdump.Dump(hookdump.Options{
		Root:  ".",
		Event: *eventName,
		Agent: *agentName,
		In:    os.Stdin,
		Out:   os.Stdout,
		Err:   os.Stderr,
	})
}
