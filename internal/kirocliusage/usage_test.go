package kirocliusage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const usageFixture = "Estimated Usage | resets on 2026-06-01 | KIRO FREE\n" +
	"Credits (6.72 of 50 covered in plan)\n" +
	"██████████ 13%\n" +
	"Overages: Disabled\n"

func TestParseUsageOutput(t *testing.T) {
	input := "\x1b[38;5;11mWARNING: \x1b[0mAgent specifies model 'claude-opus-4.6' which is not available.\n\n" +
		"\x1b[1mEstimated Usage\x1b[0m | resets on 2026-06-01 | \x1b[38;5;141mKIRO FREE\x1b[0m\n" +
		"\x1b[1mCredits\x1b[0m (6.72 of 50 covered in plan)\n" +
		"\x1b[38;5;141m██████████\x1b[38;5;244m████████████\x1b[0m 13%\n\n" +
		"Overages: \x1b[1mDisabled\x1b[0m\n" +
		"\x1b[1G\x1b[0m\x1b[?25h"

	got, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.ResetDate != "2026-06-01" {
		t.Errorf("ResetDate = %q", got.ResetDate)
	}
	if got.Plan != "KIRO FREE" {
		t.Errorf("Plan = %q", got.Plan)
	}
	if got.UsedCredits != 6.72 {
		t.Errorf("UsedCredits = %v", got.UsedCredits)
	}
	if got.TotalCredits != 50 {
		t.Errorf("TotalCredits = %v", got.TotalCredits)
	}
	if got.Percent != 13 {
		t.Errorf("Percent = %v", got.Percent)
	}
	if got.Overages != "Disabled" {
		t.Errorf("Overages = %q", got.Overages)
	}
	if got.CreditLabel() != "6.72 / 50" {
		t.Errorf("CreditLabel() = %q", got.CreditLabel())
	}
	if got.PercentLabel() != "13%" {
		t.Errorf("PercentLabel() = %q", got.PercentLabel())
	}
	if got.MetaLabel() != "KIRO FREE · overages disabled" {
		t.Errorf("MetaLabel() = %q", got.MetaLabel())
	}
}

func TestParseUsageOutputIgnoresUnrelatedPercentLines(t *testing.T) {
	input := usageFixture + "\nWARN: retry 99% complete\n"
	got, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Percent != 13 {
		t.Fatalf("Percent = %v, want 13", got.Percent)
	}
}

func TestParseUsageOutputRequiresHeaderAndCredits(t *testing.T) {
	if _, err := Parse("Overages: Disabled"); err == nil {
		t.Fatal("Parse() error = nil, want error")
	}
}

func TestUsageLabelsFormatLargePlanCredits(t *testing.T) {
	u := Usage{UsedCredits: 6.72, TotalCredits: 10000, Percent: 0.07}
	if got := u.CreditLabel(); got != "6.72 / 10,000" {
		t.Fatalf("CreditLabel() = %q, want %q", got, "6.72 / 10,000")
	}
	if got := u.PercentLabel(); got != "0.07%" {
		t.Fatalf("PercentLabel() = %q, want %q", got, "0.07%")
	}
}

func TestParseUsageOutputWithCommaSeparatedPlanCredits(t *testing.T) {
	input := "Estimated Usage | resets on 2026-06-01 | KIRO POWER\n" +
		"Credits (6.72 of 10,000 covered in plan)\n" +
		"█ 0.07%\n" +
		"Overages: Disabled\n"
	got, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.TotalCredits != 10000 {
		t.Fatalf("TotalCredits = %v, want 10000", got.TotalCredits)
	}
	if got.CreditLabel() != "6.72 / 10,000" {
		t.Fatalf("CreditLabel() = %q", got.CreditLabel())
	}
}

func TestReaderReadUnavailableWhenCommandMissing(t *testing.T) {
	cleanup := stubLookPath(func(string) (string, error) { return "", exec.ErrNotFound })
	t.Cleanup(cleanup)

	usage, ok, err := NewReader().Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if ok {
		t.Fatalf("Read() ok = true, usage = %+v", usage)
	}
}

func TestReaderReadInvokesKiroUsageCommand(t *testing.T) {
	cleanupLookPath := stubLookPath(func(name string) (string, error) {
		if name != commandName {
			t.Fatalf("LookPath(%q), want %q", name, commandName)
		}
		return "/fake/kiro-cli", nil
	})
	t.Cleanup(cleanupLookPath)

	var gotName string
	var gotArgs []string
	cleanupCommand := stubCommandContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return helperCommand(ctx, 0, usageFixture, "")
	})
	t.Cleanup(cleanupCommand)

	got, ok, err := NewReader().Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !ok {
		t.Fatal("Read() ok = false, want true")
	}
	if gotName != commandName {
		t.Fatalf("command name = %q, want %q", gotName, commandName)
	}
	wantArgs := []string{"chat", "--no-interactive", "/usage"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("command args = %#v, want %#v", gotArgs, wantArgs)
	}
	if got.CreditLabel() != "6.72 / 50" {
		t.Fatalf("CreditLabel() = %q", got.CreditLabel())
	}
}

func TestReaderReadReturnsCommandError(t *testing.T) {
	cleanupLookPath := stubLookPath(func(string) (string, error) { return "/fake/kiro-cli", nil })
	t.Cleanup(cleanupLookPath)
	cleanupCommand := stubCommandContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return helperCommand(ctx, 23, "", "boom")
	})
	t.Cleanup(cleanupCommand)

	_, ok, err := NewReader().Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Read() error = %v, want stderr detail", err)
	}
	if ok {
		t.Fatal("Read() ok = true, want false")
	}
}

func TestReaderReadReturnsParseError(t *testing.T) {
	cleanupLookPath := stubLookPath(func(string) (string, error) { return "/fake/kiro-cli", nil })
	t.Cleanup(cleanupLookPath)
	cleanupCommand := stubCommandContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return helperCommand(ctx, 0, "not usage output", "")
	})
	t.Cleanup(cleanupCommand)

	_, ok, err := NewReader().Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing usage header") {
		t.Fatalf("Read() error = %v, want parse error", err)
	}
	if ok {
		t.Fatal("Read() ok = true, want false")
	}
}

func TestReaderReadRespectsTimeout(t *testing.T) {
	cleanupLookPath := stubLookPath(func(string) (string, error) { return "/fake/kiro-cli", nil })
	t.Cleanup(cleanupLookPath)
	cleanupCommand := stubCommandContext(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return helperCommand(ctx, 0, usageFixture, "", "200ms")
	})
	t.Cleanup(cleanupCommand)

	_, ok, err := (Reader{Timeout: 10 * time.Millisecond}).Read(context.Background())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Read() error = %v, want deadline exceeded", err)
	}
	if ok {
		t.Fatal("Read() ok = true, want false")
	}
}

func stubLookPath(fn func(string) (string, error)) func() {
	orig := lookPath
	lookPath = fn
	return func() { lookPath = orig }
}

func stubCommandContext(fn func(context.Context, string, ...string) *exec.Cmd) func() {
	orig := commandContext
	commandContext = fn
	return func() { commandContext = orig }
}

func helperCommand(ctx context.Context, exitCode int, stdout, stderr string, sleep ...string) *exec.Cmd {
	args := []string{"-test.run=TestHelperProcess", "--"}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"GO_HELPER_EXIT="+strconv.Itoa(exitCode),
		"GO_HELPER_STDOUT="+stdout,
		"GO_HELPER_STDERR="+stderr,
	)
	if len(sleep) > 0 {
		cmd.Env = append(cmd.Env, "GO_HELPER_SLEEP="+sleep[0])
	}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if d := os.Getenv("GO_HELPER_SLEEP"); d != "" {
		dur, err := time.ParseDuration(d)
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(2)
		}
		time.Sleep(dur)
	}
	_, _ = os.Stdout.WriteString(os.Getenv("GO_HELPER_STDOUT"))
	_, _ = os.Stderr.WriteString(os.Getenv("GO_HELPER_STDERR"))
	if code := os.Getenv("GO_HELPER_EXIT"); code != "" && code != "0" {
		if code == "23" {
			os.Exit(23)
		}
		os.Exit(1)
	}
	os.Exit(0)
}
