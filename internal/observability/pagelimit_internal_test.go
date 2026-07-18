package observability

import "testing"

// TestPageLimit checks that a received-pdus limit is always resolved to a positive,
// capped page size, so a malformed or hostile limit cannot dump the whole buffer.
func TestPageLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want int
	}{
		{"", maxPDUPage},
		{"abc", maxPDUPage},
		{"0", maxPDUPage},
		{"-1", maxPDUPage},
		{"99999", maxPDUPage},
		{"1", 1},
		{"50", 50},
	}
	for _, tc := range tests {
		if got := pageLimit(tc.in); got != tc.want {
			t.Errorf("pageLimit(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
