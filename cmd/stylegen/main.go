// Command stylegen parses DESIGN.md YAML front matter and rewrites the :root
// block of a target CSS file with the colors tokens defined there.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kapmcli/kapm/internal/stylegen"
)

// writeAtomic writes data to path via a temp file in the same directory,
// preserving the original on failure.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".stylegen-*.tmp")
	if err != nil {
		return fmt.Errorf("stylegen temp create: %w", err)
	}
	tmp := f.Name()
	cleanup := func() { _ = os.Remove(tmp) }
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("stylegen temp write: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("stylegen temp close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		cleanup()
		return fmt.Errorf("stylegen rename %q: %w", path, err)
	}
	return nil
}

func run(inPath, outPath string) error {
	src, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("stylegen read %q: %w", inPath, err)
	}
	d, err := stylegen.ParseDesignMD(src)
	if err != nil {
		return err
	}
	css, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("stylegen read %q: %w", outPath, err)
	}
	out, err := stylegen.GenerateStyleCSS(css, d)
	if err != nil {
		return err
	}
	if bytes.Equal(css, out) {
		return nil
	}
	return writeAtomic(outPath, out)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("")
	in := flag.String("in", "DESIGN.md", "path to DESIGN.md")
	out := flag.String("out", "", "path to style.css to rewrite")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "stylegen: -out is required")
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
