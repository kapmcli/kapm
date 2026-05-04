package power

import (
	"path/filepath"
	"time"

	"github.com/kapmcli/kapm/internal/paths"
)

const (
	DefaultTimeout      = 5 * time.Minute
	SourceTypeKiroPower = "kiro-power"
)

type SourceKind string

const (
	SourceLocal        SourceKind = "local"
	SourceGitRoot      SourceKind = "git-root"
	SourceGitHubSubdir SourceKind = "github-subdir"
)

type PowerSource struct {
	Kind       SourceKind
	Path       string
	URL        string
	Owner      string
	Repo       string
	Ref        string
	PathInRepo string
}

type InstallOptions struct {
	Source    PowerSource
	TargetDir string
	Force     bool
	Timeout   time.Duration
}

// NewInstallOptions builds InstallOptions from a user source and Power-specific
// flags. CLI packages remain responsible for parsing flags, target expansion,
// and user-facing output.
func NewInstallOptions(sourceArg, targetDir, ref string, force bool, timeout time.Duration) (InstallOptions, error) {
	source, err := ParsePowerSource(sourceArg)
	if err != nil {
		return InstallOptions{}, err
	}
	if ref != "" && source.Kind != SourceLocal {
		source.Ref = ref
	}
	return InstallOptions{
		Source:    source,
		TargetDir: targetDir,
		Force:     force,
		Timeout:   timeout,
	}, nil
}

type Result struct {
	Name           string
	PowerDir       string
	ResourcePaths  []string
	MCPConfigPath  string
	HooksDir       string
	ResolvedCommit string
	Warnings       []string
	Skipped        bool
}

type SourceMeta struct {
	Type       string
	Repo       string
	Ref        string
	Commit     string
	PathInRepo string
}

func PowerDir(targetDir, name string) string {
	return filepath.Join(targetDir, paths.KiroDir, "powers", name)
}

func PowerDocPath(targetDir, name string) string {
	return filepath.Join(PowerDir(targetDir, name), "POWER.md")
}

func PowerMCPPath(targetDir, name string) string {
	return filepath.Join(PowerDir(targetDir, name), paths.MCPFile)
}

func PowerHooksDir(targetDir, name string) string {
	return filepath.Join(PowerDir(targetDir, name), paths.HooksSubdir)
}
