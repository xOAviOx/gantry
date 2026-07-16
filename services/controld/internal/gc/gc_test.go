package gc

import "testing"

func TestParseBytes(t *testing.T) {
	const def = int64(20_000_000_000)
	cases := []struct {
		in   string
		want int64
	}{
		{"20GB", 20_000_000_000},
		{"1.5GB", 1_500_000_000},
		{"512MB", 512_000_000},
		{"1KB", 1_000},
		{"1GiB", 1 << 30},
		{"512MiB", 512 << 20},
		{"1073741824", 1073741824},
		{"  2GB  ", 2_000_000_000},
		{"10gb", 10_000_000_000}, // case-insensitive
		{"", def},
		{"nonsense", def},
		{"GB", def}, // no number
	}
	for _, c := range cases {
		if got := ParseBytes(c.in, def); got != c.want {
			t.Errorf("ParseBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
