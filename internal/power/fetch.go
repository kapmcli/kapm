package power

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Fetcher resolves a PowerSource to a local directory ready for installation.
// localDir is the path to the fetched content; commit is the resolved VCS
// commit (empty for local sources); cleanup releases any temporary resources
// created during the fetch (e.g. a cloned temp dir).
type Fetcher interface {
	Fetch(ctx context.Context, src PowerSource) (localDir string, commit string, cleanup func(), err error)
}

// newGitCommand is a package-level variable so tests can swap it out.
// It always sets GIT_TERMINAL_PROMPT=0 to prevent git from blocking on
// interactive credential prompts in non-interactive environments.
var newGitCommand = func(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// localFetcher implements Fetcher for SourceLocal: the path is used as-is,
// no commit is resolved, and cleanup is a no-op.
type localFetcher struct{}

func (localFetcher) Fetch(_ context.Context, src PowerSource) (string, string, func(), error) {
	return src.Path, "", func() {}, nil
}

// gitFetcher implements Fetcher for git-backed sources (SourceGitRoot,
// SourceGitHubSubdir).
type gitFetcher struct{}

var commitSHARegex = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// gitRefRegex allows characters legal in git refs but NOT a leading '-'
// which git would interpret as an option flag.
var gitRefRegex = regexp.MustCompile(`^[A-Za-z0-9._/\-]+$`)

func validateGitRef(ref string) error {
	if ref == "" {
		return errors.New("git ref is empty")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("git ref %q starts with '-' (would be interpreted as a flag)", ref)
	}
	if !gitRefRegex.MatchString(ref) {
		return fmt.Errorf("git ref %q contains invalid characters", ref)
	}
	return nil
}

const largeRepoWarningBytes = 100 << 20

func (gitFetcher) Fetch(ctx context.Context, src PowerSource) (string, string, func(), error) {
	tempDir, err := os.MkdirTemp("", "kapm-power-*")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	repoDir, localDir, err := fetchGitSource(ctx, tempDir, src)
	if err != nil {
		cleanup()
		return "", "", func() {}, err
	}

	commit, err := gitOutput(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("resolve git commit: %w", err)
	}
	if _, err := os.Stat(localDir); err != nil {
		cleanup()
		if errors.Is(err, fs.ErrNotExist) && src.PathInRepo != "" {
			return "", "", func() {}, fmt.Errorf("subpath %q not found in repository %q", src.PathInRepo, src.URL)
		}
		return "", "", func() {}, fmt.Errorf("stat fetched path %q: %w", localDir, err)
	}

	warnIfLargeRepo(repoDir)
	return localDir, strings.TrimSpace(commit), cleanup, nil
}

func fetchGitSource(ctx context.Context, tempDir string, src PowerSource) (repoDir string, localDir string, err error) {
	if src.Kind == SourceGitHubSubdir && src.PathInRepo != "" {
		repoDir = filepath.Join(tempDir, "sparse")
		localDir, err = fetchSparseCheckout(ctx, repoDir, src)
		if err != nil {
			return "", "", err
		}
		return repoDir, localDir, nil
	}

	repoDir = filepath.Join(tempDir, "repo")
	localDir, err = fetchFullClone(ctx, repoDir, src)
	if err != nil {
		return "", "", err
	}
	return repoDir, localDir, nil
}

func fetchSparseCheckout(ctx context.Context, repoDir string, src PowerSource) (string, error) {
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return "", fmt.Errorf("create sparse checkout dir %q: %w", repoDir, err)
	}

	remoteURL := gitRemoteURL(src)
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if err := validateGitRef(ref); err != nil {
		return "", err
	}

	commands := [][]string{
		{"init"},
		{"remote", "add", "origin", "--", remoteURL},
		{"sparse-checkout", "init", "--cone"},
		{"sparse-checkout", "set", src.PathInRepo},
		{"fetch", "--depth=1", "origin", ref},
		{"checkout", "FETCH_HEAD"},
	}
	for _, args := range commands {
		if err := gitRun(ctx, repoDir, args...); err != nil {
			return "", err
		}
	}

	return filepath.Join(repoDir, filepath.FromSlash(src.PathInRepo)), nil
}

func fetchFullClone(ctx context.Context, repoDir string, src PowerSource) (string, error) {
	if src.Ref != "" {
		if err := validateGitRef(src.Ref); err != nil {
			return "", err
		}
	}
	cloneArgs := []string{"clone", "--depth=1"}
	if src.Ref != "" && !looksLikeCommitSHA(src.Ref) {
		cloneArgs = append(cloneArgs, "--branch", src.Ref)
	}
	cloneArgs = append(cloneArgs, "--", gitRemoteURL(src), repoDir)
	if err := gitRun(ctx, "", cloneArgs...); err != nil {
		return "", err
	}

	if src.Ref != "" && looksLikeCommitSHA(src.Ref) {
		if err := gitRun(ctx, repoDir, "fetch", "--depth=1", "origin", src.Ref); err != nil {
			return "", err
		}
		if err := gitRun(ctx, repoDir, "checkout", src.Ref); err != nil {
			return "", err
		}
	}

	localDir := repoDir
	if src.PathInRepo != "" {
		localDir = filepath.Join(repoDir, filepath.FromSlash(src.PathInRepo))
	}
	return localDir, nil
}

func gitError(args []string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
}

func gitRun(ctx context.Context, dir string, args ...string) error {
	cmd := newGitCommand(ctx, dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return gitError(args, err, stderr.String())
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := newGitCommand(ctx, dir, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", gitError(args, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func gitRemoteURL(src PowerSource) string {
	if src.Kind == SourceGitHubSubdir {
		return fmt.Sprintf("https://github.com/%s/%s.git", src.Owner, src.Repo)
	}
	return src.URL
}

func looksLikeCommitSHA(ref string) bool {
	return commitSHARegex.MatchString(strings.TrimSpace(ref))
}

func warnIfLargeRepo(root string) {
	size, err := directorySize(root)
	if err != nil {
		return
	}
	if size > largeRepoWarningBytes {
		slog.Warn("power repository checkout is large", "path", root, "bytes", size)
	}
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
