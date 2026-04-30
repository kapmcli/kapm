package monitor

import (
	"fmt"
)

const tsLayout = "2006-01-02 15:04:05"

const (
	overviewTopN      = 10
	maxRecentSessions = 10
)

// splitBoxWidths returns n outer-widths that sum to total, separated by gap
// cells between boxes. Remainder is distributed to leftmost boxes.
func splitBoxWidths(total, n, gap int) []int {
	if n <= 0 {
		return nil
	}
	avail := max(total-gap*(n-1), n*10)
	base := avail / n
	rem := avail - base*n
	out := make([]int, n)
	for i := range out {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}

// interiorOf returns the interior content width of a box with outer width w.
// Border + padding consume 4 cells.
func interiorOf(w int) int {
	if w-4 < 10 {
		return 10
	}
	return w - 4
}

func errorCountText(n int) string {
	if n == 0 {
		return mutedStyle.Render("0")
	}
	return errorStyle.Render(fmt.Sprintf("%d", n))
}

func formatErrRate(r float64) string {
	pct := r * 100
	switch {
	case pct >= 10:
		return errorStyle.Render(fmt.Sprintf("%5.1f%%", pct))
	case pct > 0:
		return warnStyle.Render(fmt.Sprintf("%5.1f%%", pct))
	}
	return mutedStyle.Render("  0.0%")
}

func (m *model) contentWidth() int {
	// The block width passed to borderStyle.Width(...). Border + padding consume
	// 4 cells, and we want the block to fit within (terminal width - 4).
	w := min(max(m.width-4, 60), 200)
	return w
}

// interiorWidth is the usable content width inside contentWidth() (minus border+padding).
func (m *model) interiorWidth() int {
	w := max(m.contentWidth()-4, 40)
	return w
}

// clampOffset returns the list offset so the cursor is visible within a window of `rows`.
func clampOffset(cursor, total, rows int) int {
	if total <= rows || cursor < rows {
		return 0
	}
	start := cursor - rows + 1
	if start+rows > total {
		start = total - rows
	}
	if start < 0 {
		start = 0
	}
	return start
}
