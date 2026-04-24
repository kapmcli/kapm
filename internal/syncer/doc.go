// Package syncer coordinates scanning input primitives and writing .kiro output.
//
// Source discovery under apm_modules/<org>/<repo>/ prefers a .apm/ subdir when
// present (full-package install) and otherwise uses the package root itself
// when it contains any known APM primitive subdir such as skills/, prompts/,
// instructions/, agents/, chatmodes/, or commands/ (virtual-package install).
//
// Unknown fields in apm.yml are rejected at parse time.
package syncer
