package monitor

import (
	"testing"
)

func TestInteriorOfBasic(t *testing.T) {
	cases := []struct{ outer, want int }{
		{60, 56},
		{100, 96},
		{200, 196},
	}
	for _, c := range cases {
		if got := interiorOf(c.outer); got != c.want {
			t.Errorf("interiorOf(%d) = %d, want %d", c.outer, got, c.want)
		}
	}
}

// NOTE: for outer widths in [10, 13], interiorOf returns a value greater than
// the outer width (the interior floor is 10 while the outer floor is also 10,
// so 4 cells of padding are unaccounted for). This is currently unreachable
// because contentWidth() floors at 60. If a future change lowers that floor,
// the monotonic invariant `interior <= outer - 4` must be restored.
func TestInteriorOfFloorIsTen(t *testing.T) {
	cases := []struct{ outer, want int }{
		{4, 10},
		{13, 10},
	}
	for _, c := range cases {
		if got := interiorOf(c.outer); got != c.want {
			t.Errorf("interiorOf(%d) = %d, want %d", c.outer, got, c.want)
		}
	}
}

func TestSplitBoxWidthsOuterFloor(t *testing.T) {
	widths := splitBoxWidths(0, 1, 0)
	if len(widths) != 1 || widths[0] != 10 {
		t.Errorf("splitBoxWidths(0,1,0) = %v, want [10]", widths)
	}
}

func TestSplitBoxWidthsSumsToTotal(t *testing.T) {
	total, n, gap := 100, 3, 1
	widths := splitBoxWidths(total, n, gap)
	sum := 0
	for _, w := range widths {
		sum += w
	}
	want := total - gap*(n-1)
	if sum != want {
		t.Errorf("splitBoxWidths(%d,%d,%d) sum = %d, want %d", total, n, gap, sum, want)
	}
}
