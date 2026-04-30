package power

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ParsePowerSource classifies a user-provided source string without touching the filesystem.
func ParsePowerSource(input string) (PowerSource, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return PowerSource{}, errors.New("power source cannot be empty")
	}

	if strings.Contains(raw, "/-/tree/") {
		return PowerSource{}, errors.New("GitLab subdir URL not supported in MVP; use root URL")
	}

	if strings.Contains(raw, "/src/branch/") {
		return PowerSource{}, errors.New("Gitea/Codeberg subdir URL not supported in MVP; use root URL")
	}

	if source, ok := parseGitHubSubdirSource(raw); ok {
		return source, nil
	}

	if isLocalSource(raw) {
		path, err := expandLocalSource(raw)
		if err != nil {
			return PowerSource{}, fmt.Errorf("expand local source: %w", err)
		}
		return PowerSource{Kind: SourceLocal, Path: path}, nil
	}

	if source, ok := parseGitHubShorthandSource(raw); ok {
		return source, nil
	}

	if isGitRootSource(raw) {
		return PowerSource{Kind: SourceGitRoot, URL: raw}, nil
	}

	return PowerSource{}, fmt.Errorf("unrecognized source: %s", raw)
}

func isLocalSource(raw string) bool {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
		return false
	}
	switch {
	case raw == "~", raw == ".", raw == "..":
		return true
	case strings.HasPrefix(raw, "~/"), strings.HasPrefix(raw, "./"), strings.HasPrefix(raw, "../"), strings.HasPrefix(raw, "/"):
		return true
	default:
		return !strings.Contains(raw, "/")
	}
}

func isGitRootSource(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "git://") || strings.HasPrefix(raw, "git@")
}

func expandLocalSource(raw string) (string, error) {
	if raw == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(raw, "~/"))), nil
	}
	return raw, nil
}

func parseGitHubSubdirSource(raw string) (PowerSource, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return PowerSource{}, false
	}

	if !isGitHubHost(parsed.Host) {
		return PowerSource{}, false
	}

	trimmedPath := strings.TrimRight(parsed.Path, "/")
	beforeTree, afterTree, ok := strings.Cut(trimmedPath, "/tree/")
	if !ok {
		return PowerSource{}, false
	}

	repoPath := strings.TrimRight(beforeTree, "/")
	owner, repo, ok := splitOwnerRepo(repoPath)
	if !ok {
		return PowerSource{}, false
	}

	afterTree = strings.TrimPrefix(afterTree, "/")
	if afterTree == "" {
		return PowerSource{}, false
	}

	ref, pathInRepo, _ := strings.Cut(afterTree, "/")
	pathInRepo = strings.TrimRight(pathInRepo, "/")
	if !validPathInRepo(pathInRepo) {
		return PowerSource{}, false
	}
	rootURL := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/" + owner + "/" + repo}).String()

	return PowerSource{
		Kind:       SourceGitHubSubdir,
		URL:        rootURL,
		Owner:      owner,
		Repo:       repo,
		Ref:        ref,
		PathInRepo: pathInRepo,
	}, true
}

func parseGitHubShorthandSource(raw string) (PowerSource, bool) {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
		return PowerSource{}, false
	}

	trimmed := strings.Trim(raw, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return PowerSource{}, false
	}
	if !isGitHubShorthandSegment(parts[0]) || !isGitHubShorthandSegment(parts[1]) {
		return PowerSource{}, false
	}

	rootURL := "https://github.com/" + parts[0] + "/" + parts[1]
	if len(parts) == 2 {
		return PowerSource{
			Kind:  SourceGitRoot,
			URL:   rootURL,
			Owner: parts[0],
			Repo:  parts[1],
		}, true
	}

	if parts[2] == "tree" {
		if len(parts) < 4 || parts[3] == "" {
			return PowerSource{}, false
		}
		pathInRepo := strings.Join(parts[4:], "/")
		if !validPathInRepo(pathInRepo) {
			return PowerSource{}, false
		}
		return PowerSource{
			Kind:       SourceGitHubSubdir,
			URL:        rootURL,
			Owner:      parts[0],
			Repo:       parts[1],
			Ref:        parts[3],
			PathInRepo: pathInRepo,
		}, true
	}

	pathInRepo := strings.Join(parts[2:], "/")
	if !validPathInRepo(pathInRepo) {
		return PowerSource{}, false
	}
	return PowerSource{
		Kind:       SourceGitHubSubdir,
		URL:        rootURL,
		Owner:      parts[0],
		Repo:       parts[1],
		PathInRepo: pathInRepo,
	}, true
}

func validPathInRepo(p string) bool {
	if p == "" {
		return true
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == ".." || seg == "." {
			return false
		}
	}
	return true
}

func splitOwnerRepo(repoPath string) (string, string, bool) {
	parts := strings.Split(strings.Trim(repoPath, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isGitHubHost(host string) bool {
	switch strings.ToLower(strings.TrimPrefix(host, "www.")) {
	case "github.com":
		return true
	default:
		return false
	}
}

func isGitHubShorthandSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '-', '_':
			continue
		default:
			return false
		}
	}
	return true
}
