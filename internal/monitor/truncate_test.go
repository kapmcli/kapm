package monitor

import "testing"

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel"},
		{"日本語", 9, "日本語"},   // 3 runes × 3 bytes = 9
		{"日本語", 8, "日本"},    // can't fit 3rd rune
		{"日本語", 6, "日本"},
		{"日本語", 3, "日"},
		{"日本語", 2, ""},      // can't fit even 1 rune at 2 bytes
		{"a🎉b", 5, "a🎉"},   // 🎉 is 4 bytes; a(1)+🎉(4)=5
		{"a🎉b", 4, "a"},     // can't fit 🎉 at offset 4
		{"", 10, ""},
	}
	for _, tt := range tests {
		got := truncateUTF8(tt.in, tt.max)
		if got != tt.want {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
}
