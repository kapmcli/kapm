//go:build windows

package hook

import "os"

func flockExclusive(_ *os.File) error { return nil }
func flockUnlock(_ *os.File)          {}
func flockRotate(_ *os.File) error    { return nil }
