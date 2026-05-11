package monitor

import (
	"fmt"
)

const tsLayout = "2006-01-02 15:04:05"

const maxRecentSessions = 10

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

// overviewParams holds the display parameters for the Overview tab.
type overviewParams struct {
	topN         int
	recentN      int
	showActivity bool
	showRow2     bool
	columns      int
}

// overviewLayout computes overviewParams from the current terminal dimensions.
func (m *model) overviewLayout() overviewParams {
	avail := m.height - 6

	var p overviewParams
	switch {
	case avail >= 44:
		p = overviewParams{topN: 10, recentN: 10, showActivity: true, showRow2: true}
	case avail >= 34:
		p = overviewParams{topN: 5, recentN: 5, showActivity: true, showRow2: true}
	case avail >= 24:
		p = overviewParams{topN: 5, recentN: 3, showActivity: false, showRow2: true}
	default: // < 24 (covers 18-23 and < 18)
		p = overviewParams{topN: 5, recentN: 3, showActivity: false, showRow2: false}
	}

	switch {
	case m.width >= 100:
		p.columns = 3
	case m.width >= 80:
		p.columns = 2
	default:
		p.columns = 1
		if p.topN > 3 {
			p.topN = 3
		}
		if p.recentN > 3 {
			p.recentN = 3
		}
	}

	return p
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
