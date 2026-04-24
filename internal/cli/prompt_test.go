package cli_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/cli"
)

func TestPrompterAskUsesDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := cli.NewPrompter(strings.NewReader("\n"), &out)

	got, err := p.Ask("name", "default-name")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if got != "default-name" {
		t.Fatalf("Ask() = %q, want %q", got, "default-name")
	}
}

func TestPrompterSelectParsesSelection(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := cli.NewPrompter(strings.NewReader("2\n"), &out)

	got, err := p.Select("model", []string{"a", "b", "c"}, 0)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got != "b" {
		t.Fatalf("Select() = %q, want %q", got, "b")
	}
}

func TestPrompterMultiSelectDefaultsAll(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := cli.NewPrompter(strings.NewReader("\n"), &out)

	got, err := p.MultiSelect("tools", []string{"fs_read", "fs_write", "glob"}, true)
	if err != nil {
		t.Fatalf("MultiSelect() error = %v", err)
	}

	want := []string{"fs_read", "fs_write", "glob"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MultiSelect() = %#v, want %#v", got, want)
	}
}

func TestPrompterMultiSelectWithDefaultsUsesSubset(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := cli.NewPrompter(strings.NewReader("\n"), &out)

	got, err := p.MultiSelectWithDefaults("allowedTools", []string{"fs_read", "fs_write", "glob"}, []int{0, 2})
	if err != nil {
		t.Fatalf("MultiSelectWithDefaults() error = %v", err)
	}

	want := []string{"fs_read", "glob"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MultiSelectWithDefaults() = %#v, want %#v", got, want)
	}
}

func TestPrompterMultiInputCollectsValues(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := cli.NewPrompter(strings.NewReader("file://AGENTS.md\nskill://planner\n\n"), &out)

	got, err := p.MultiInput("resources")
	if err != nil {
		t.Fatalf("MultiInput() error = %v", err)
	}

	want := []string{"file://AGENTS.md", "skill://planner"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MultiInput() = %#v, want %#v", got, want)
	}
}

func TestPrompterConfirmUsesDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := cli.NewPrompter(strings.NewReader("\n"), &out)

	got, err := p.Confirm("continue", true)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !got {
		t.Fatal("Confirm() = false, want true")
	}
}
