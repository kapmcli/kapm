package monitor

import (
	"fmt"
	"strings"
)

// Column describes a single column header in a list view.
type Column struct {
	Header string
	Width  int
	Right  bool // right-aligned header
}

// listViewOpts holds the parameters for renderListView.
type listViewOpts struct {
	columns []Column
	// rows contains pre-formatted cell strings per row.
	// Each cell must already be padded/aligned to its Column.Width.
	rows   [][]string
	cursor int // selected row index; -1 for non-interactive
	gap    int // spaces between columns (default 2 if 0)
	// width overrides m.contentWidth() for the border box (0 = use default).
	width int
	// prefix is rendered before the header (e.g. section title). Must include trailing newline.
	prefix string
}

// renderListView renders a standard TUI list: header row, separator, viewport
// rows with cursor highlighting, and a "showing N/M" footer.
// cursor = -1 for non-interactive mode (no highlight, no scroll, no footer).
func (m *model) renderListView(opts listViewOpts) string {
	boxWidth := opts.width
	if boxWidth == 0 {
		boxWidth = m.contentWidth()
	}
	interior := max(boxWidth-4, 10)

	gap := opts.gap
	if gap == 0 {
		gap = 2
	}
	gapStr := strings.Repeat(" ", gap)

	var b strings.Builder

	// Optional prefix (section header)
	if opts.prefix != "" {
		b.WriteString(opts.prefix)
	}

	// Header
	b.WriteString("  ")
	for i, col := range opts.columns {
		if i > 0 {
			b.WriteString(gapStr)
		}
		if col.Right {
			fmt.Fprintf(&b, "%*s", col.Width, col.Header)
		} else {
			fmt.Fprintf(&b, "%-*s", col.Width, col.Header)
		}
	}
	b.WriteString("\n")

	// Separator
	b.WriteString(mutedStyle.Render(strings.Repeat("─", interior)))
	b.WriteString("\n")

	total := len(opts.rows)
	interactive := opts.cursor >= 0

	// Viewport bounds
	var start, end int
	if interactive {
		rows := m.viewportHeight()
		start = clampOffset(opts.cursor, total, rows)
		end = min(start+rows, total)
	} else {
		start = 0
		end = total
	}

	// Rows
	for i := start; i < end; i++ {
		var row strings.Builder
		row.WriteString("  ")
		for j, cell := range opts.rows[i] {
			if j > 0 {
				row.WriteString(gapStr)
			}
			row.WriteString(cell)
		}
		line := row.String()
		if interactive && i == opts.cursor {
			b.WriteString(selectedStyle.Render("▸ " + line[2:]))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	// Footer
	if interactive {
		fmt.Fprintf(&b, "\n%s  %d/%d", mutedStyle.Render("showing"), end-start, total)
	}

	return borderStyle.Width(boxWidth).Render(b.String())
}
