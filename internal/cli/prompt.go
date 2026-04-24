package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Prompter wraps I/O for interactive prompts.
type Prompter struct {
	r *bufio.Reader
	w io.Writer
}

func (p *Prompter) printf(format string, args ...any) error {
	_, err := fmt.Fprintf(p.w, format, args...)
	return err
}

func (p *Prompter) print(text string) error {
	_, err := fmt.Fprint(p.w, text)
	return err
}

// NewPrompter returns a Prompter backed by the provided reader and writer.
func NewPrompter(r io.Reader, w io.Writer) *Prompter {
	if r == nil {
		r = os.Stdin
	}
	if w == nil {
		w = os.Stdout
	}

	return &Prompter{r: bufio.NewReader(r), w: w}
}

// Ask prompts for a single-line text input.
func (p *Prompter) Ask(label, defaultValue string) (string, error) {
	if defaultValue == "" {
		if err := p.printf("%s: ", label); err != nil {
			return "", err
		}
	} else {
		if err := p.printf("%s [%s]: ", label, defaultValue); err != nil {
			return "", err
		}
	}

	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return defaultValue, nil
	}

	return line, nil
}

// Select presents a numbered list and returns the selected item.
func (p *Prompter) Select(label string, options []string, defaultIndex int) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("select %q: no options provided", label)
	}
	if defaultIndex < 0 || defaultIndex >= len(options) {
		return "", fmt.Errorf("select %q: invalid default index %d", label, defaultIndex)
	}

	if err := p.printf("%s:\n", label); err != nil {
		return "", err
	}
	for i, option := range options {
		if err := p.printf("  %d. %s\n", i+1, option); err != nil {
			return "", err
		}
	}
	if err := p.printf("Enter selection [%d]: ", defaultIndex+1); err != nil {
		return "", err
	}

	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return options[defaultIndex], nil
	}

	index, err := strconv.Atoi(line)
	if err != nil {
		return "", fmt.Errorf("invalid selection %q", line)
	}
	if index < 1 || index > len(options) {
		return "", fmt.Errorf("selection %d out of range", index)
	}

	return options[index-1], nil
}

// MultiSelect presents a numbered list and returns the selected items.
func (p *Prompter) MultiSelect(label string, options []string, defaultAll bool) ([]string, error) {
	defaultIndices := []int(nil)
	if defaultAll {
		defaultIndices = make([]int, len(options))
		for i := range options {
			defaultIndices[i] = i
		}
	}

	return p.MultiSelectWithDefaults(label, options, defaultIndices)
}

// MultiSelectWithDefaults presents a numbered list and returns the selected items.
func (p *Prompter) MultiSelectWithDefaults(label string, options []string, defaultIndices []int) ([]string, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("multiselect %q: no options provided", label)
	}
	for _, index := range defaultIndices {
		if index < 0 || index >= len(options) {
			return nil, fmt.Errorf("multiselect %q: invalid default index %d", label, index)
		}
	}

	if err := p.printf("%s:\n", label); err != nil {
		return nil, err
	}
	for i, option := range options {
		if err := p.printf("  %d. %s\n", i+1, option); err != nil {
			return nil, err
		}
	}
	if len(defaultIndices) > 0 {
		if err := p.printf("Enter comma-separated selections [%s]: ", formatSelectionNumbers(defaultIndices)); err != nil {
			return nil, err
		}
	} else {
		if err := p.print("Enter comma-separated selections: "); err != nil {
			return nil, err
		}
	}

	line, err := p.readLine()
	if err != nil {
		return nil, err
	}
	if line == "" {
		return optionsForIndices(options, defaultIndices), nil
	}

	indices, err := parseSelectionNumbers(line, len(options))
	if err != nil {
		return nil, err
	}

	return optionsForIndices(options, indices), nil
}

// MultiInput collects multiple values until the user enters an empty line.
func (p *Prompter) MultiInput(label string) ([]string, error) {
	if err := p.printf("%s (blank line to finish):\n", label); err != nil {
		return nil, err
	}

	values := make([]string, 0)
	for {
		if err := p.print("> "); err != nil {
			return nil, err
		}
		line, err := p.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(values) > 0 {
					return values, nil
				}
				return nil, nil
			}
			return nil, err
		}
		if line == "" {
			return values, nil
		}
		values = append(values, line)
	}
}

// Confirm prompts for a yes/no answer.
func (p *Prompter) Confirm(label string, defaultValue bool) (bool, error) {
	prompt := "[y/N]"
	if defaultValue {
		prompt = "[Y/n]"
	}
	if err := p.printf("%s %s: ", label, prompt); err != nil {
		return false, err
	}

	line, err := p.readLine()
	if err != nil {
		return false, err
	}
	if line == "" {
		return defaultValue, nil
	}

	switch strings.ToLower(line) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid confirmation %q", line)
	}
}

func (p *Prompter) readLine() (string, error) {
	line, err := p.r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}

	return strings.TrimRight(line, "\r\n"), nil
}

func parseSelectionNumbers(value string, optionCount int) ([]int, error) {
	parts := strings.Split(value, ",")
	indices := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			return nil, fmt.Errorf("invalid selection %q", value)
		}

		index, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid selection %q", trimmed)
		}
		if index < 1 || index > optionCount {
			return nil, fmt.Errorf("selection %d out of range", index)
		}

		zeroIndex := index - 1
		if _, ok := seen[zeroIndex]; ok {
			continue
		}
		seen[zeroIndex] = struct{}{}
		indices = append(indices, zeroIndex)
	}

	return indices, nil
}

func optionsForIndices(options []string, indices []int) []string {
	if len(indices) == 0 {
		return nil
	}

	selected := make([]string, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, options[index])
	}

	return selected
}

func formatSelectionNumbers(indices []int) string {
	parts := make([]string, 0, len(indices))
	for _, index := range indices {
		parts = append(parts, strconv.Itoa(index+1))
	}
	return strings.Join(parts, ",")
}
