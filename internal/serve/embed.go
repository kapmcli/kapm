package serve

import "embed"

//go:generate go run ../../cmd/stylegen -in=../../DESIGN.md -out=assets/style.css
//go:generate cp ../../DESIGN.md DESIGN.md

//go:embed assets/*
var Assets embed.FS

//go:embed templates/*
var Templates embed.FS

// DesignMDRaw is the DESIGN.md source bytes embedded at build time.
// Kept in sync with the repo-root DESIGN.md by `go generate`.
//
//go:embed DESIGN.md
var DesignMDRaw []byte
