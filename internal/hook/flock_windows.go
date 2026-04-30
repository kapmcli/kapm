//go:build windows

package hook

// NOTE: CI-sensitive. The hook-handler file-lock implementation has
// historically been fragile across platforms; this current form settled
// after multiple CI failures. Do not modify without running the full
// Windows + Linux test matrix.
//
// Kiro assigns distinct session IDs to each sub-agent invocation, so
// different processes always write to different <session>.jsonl files.
// On Windows these no-op stubs are sufficient because there is no
// realistic concurrent-writer scenario for a single session log.

import (
	"os"
)

func flockExclusive(_ *os.File) error { return nil }
func flockUnlock(_ *os.File)          {}
func flockRotate(_ *os.File) error    { return nil }
