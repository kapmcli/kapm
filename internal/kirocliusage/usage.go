package kirocliusage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	commandName    = "kiro-cli"
	defaultTimeout = 10 * time.Second
)

var (
	lookPath       = exec.LookPath
	commandContext = exec.CommandContext

	headerPattern   = regexp.MustCompile(`(?i)^Estimated Usage\s*\|\s*resets on\s*([0-9]{4}-[0-9]{2}-[0-9]{2})\s*\|\s*(.+)$`)
	creditsPattern  = regexp.MustCompile(`(?i)^Credits\s*\(([-+]?[0-9][0-9,]*(?:\.[0-9]+)?)\s+of\s+([-+]?[0-9][0-9,]*(?:\.[0-9]+)?)\s+covered in plan\)`)
	percentPattern  = regexp.MustCompile(`(?:^|\s)([-+]?[0-9]+(?:\.[0-9]+)?)%$`)
	overagesPattern = regexp.MustCompile(`(?i)^Overages:\s*(.+)$`)
	ansiPattern     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

// Usage is the account-level usage summary reported by Kiro CLI's /usage command.
type Usage struct {
	ResetDate    string
	Plan         string
	UsedCredits  float64
	TotalCredits float64
	Percent      float64
	Overages     string
}

// CreditLabel formats used/covered plan credits for compact cards.
func (u Usage) CreditLabel() string {
	return formatNumber(u.UsedCredits) + " / " + formatNumber(u.TotalCredits)
}

// PercentLabel formats the usage percentage for compact cards.
func (u Usage) PercentLabel() string {
	return formatNumber(u.Percent) + "%"
}

// MetaLabel formats plan and overage state for compact cards.
func (u Usage) MetaLabel() string {
	if u.Overages == "" {
		return u.Plan
	}
	return u.Plan + " · overages " + strings.ToLower(u.Overages)
}

// Reader invokes kiro-cli and parses the account usage output.
type Reader struct {
	Timeout time.Duration
}

// NewReader returns a Reader with safe defaults.
func NewReader() Reader {
	return Reader{Timeout: defaultTimeout}
}

// Read returns ok=false when kiro-cli is unavailable. Other failures are
// returned to the caller so UI integrations can log and fail open.
func (r Reader) Read(ctx context.Context) (Usage, bool, error) {
	if _, err := lookPath(commandName); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Usage{}, false, nil
		}
		return Usage{}, false, fmt.Errorf("find %s: %w", commandName, err)
	}

	timeout := r.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := commandContext(cmdCtx, commandName, "chat", "--no-interactive", "/usage")
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "NO_COLOR=1")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if cmdCtx.Err() != nil {
			return Usage{}, false, fmt.Errorf("run %s usage: %w", commandName, cmdCtx.Err())
		}
		return Usage{}, false, fmt.Errorf("run %s usage: %w: %s", commandName, err, strings.TrimSpace(stderr.String()))
	}

	usage, err := Parse(stdout.String() + "\n" + stderr.String())
	if err != nil {
		return Usage{}, false, err
	}
	return usage, true, nil
}

// Parse extracts usage fields from the human-oriented /usage output.
func Parse(output string) (Usage, error) {
	clean := stripANSI(output)
	clean = strings.ReplaceAll(clean, "\r", "\n")

	var usage Usage
	var sawHeader, sawCredits bool
	for rawLine := range strings.SplitSeq(clean, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "WARNING:") {
			continue
		}

		if m := headerPattern.FindStringSubmatch(line); m != nil {
			usage.ResetDate = strings.TrimSpace(m[1])
			usage.Plan = strings.TrimSpace(m[2])
			sawHeader = true
			continue
		}
		if m := creditsPattern.FindStringSubmatch(line); m != nil {
			usedText := strings.ReplaceAll(m[1], ",", "")
			used, err := strconv.ParseFloat(usedText, 64)
			if err != nil {
				return Usage{}, fmt.Errorf("parse used credits %q: %w", m[1], err)
			}
			totalText := strings.ReplaceAll(m[2], ",", "")
			total, err := strconv.ParseFloat(totalText, 64)
			if err != nil {
				return Usage{}, fmt.Errorf("parse total credits %q: %w", m[2], err)
			}
			usage.UsedCredits = used
			usage.TotalCredits = total
			sawCredits = true
			continue
		}
		if strings.Contains(line, "%") && strings.Contains(line, "█") {
			if m := percentPattern.FindStringSubmatch(line); m != nil {
				pct, err := strconv.ParseFloat(m[1], 64)
				if err != nil {
					return Usage{}, fmt.Errorf("parse usage percent %q: %w", m[1], err)
				}
				usage.Percent = pct
			}
			continue
		}
		if m := overagesPattern.FindStringSubmatch(line); m != nil {
			usage.Overages = strings.TrimSpace(m[1])
		}
	}

	if !sawHeader || !sawCredits {
		return Usage{}, errors.New("parse kiro usage: missing usage header or credits line")
	}
	return usage, nil
}

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func formatNumber(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	whole, frac, hasFrac := strings.Cut(s, ".")
	sign := ""
	if strings.HasPrefix(whole, "-") || strings.HasPrefix(whole, "+") {
		sign = whole[:1]
		whole = whole[1:]
	}
	if len(whole) > 3 {
		var b strings.Builder
		b.WriteString(sign)
		prefix := len(whole) % 3
		if prefix == 0 {
			prefix = 3
		}
		b.WriteString(whole[:prefix])
		for i := prefix; i < len(whole); i += 3 {
			b.WriteByte(',')
			b.WriteString(whole[i : i+3])
		}
		whole = b.String()
	} else {
		whole = sign + whole
	}
	if hasFrac {
		return whole + "." + frac
	}
	return whole
}
