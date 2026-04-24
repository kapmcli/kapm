package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kapmcli/kapm/internal/apmconfig"
	"github.com/kapmcli/kapm/internal/fileutil"
)

func readAgentRawJSON(path string) (map[string]json.RawMessage, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	rawMap := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return nil, nil, fmt.Errorf("unmarshal %q: %w", path, err)
	}
	return rawMap, data, nil
}

func writeAgentRawJSON(path string, rawMap map[string]json.RawMessage) error {
	data, err := apmconfig.MarshalIndentedJSON(rawMap)
	if err != nil {
		return fmt.Errorf("marshal agent json: %w", err)
	}
	if _, err := fileutil.WriteFileAtomic(path, data, true); err != nil {
		return err
	}
	return nil
}

func applyDefaults(root *string, in *io.Reader, out *io.Writer) {
	if *root == "" {
		*root = "."
	}
	if *in == nil {
		*in = os.Stdin
	}
	if *out == nil {
		*out = os.Stdout
	}
}

func filterHooks(entries []json.RawMessage, keep func(json.RawMessage) bool) []json.RawMessage {
	filtered := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		if keep(e) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func defaultToolIndices(options, selected []string) []int {
	selectedSet := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		selectedSet[value] = struct{}{}
	}

	indices := make([]int, 0, len(selectedSet))
	for i, option := range options {
		if _, ok := selectedSet[option]; ok {
			indices = append(indices, i)
		}
	}

	return indices
}

func writeValidatedPair(root, pathA string, dataA []byte, pathB string, dataB []byte, force bool) (bool, error) {
	for _, dir := range []string{filepath.Dir(pathA), filepath.Dir(pathB)} {
		if err := validatePath(root, dir); err != nil {
			return false, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, fmt.Errorf("mkdir %q: %w", dir, err)
		}
	}
	return fileutil.WriteFilePair(pathA, dataA, pathB, dataB, force)
}

func validatePath(root, path string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("abs root %q: %w", root, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("abs path %q: %w", path, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return fmt.Errorf("rel path %q: %w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path %q escapes root %q", path, root)
	}

	current := absRoot
	if err := validatePathEntry(current, true); err != nil {
		return err
	}
	if rel == "." {
		return nil
	}

	for part := range strings.SplitSeq(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		if err := validatePathEntry(current, false); err != nil {
			return err
		}
	}

	return nil
}

func validatePathEntry(path string, mustExist bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !mustExist {
			return nil
		}
		return fmt.Errorf("lstat %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path %q must not be a symlink", path)
	}
	return nil
}
