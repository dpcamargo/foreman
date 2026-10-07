package addrepo

import "testing"

func TestSub(t *testing.T) {
	for _, c := range []struct{ a, b, want int }{{5, 3, 2}, {0, 4, -4}, {-2, -2, 0}} {
		if got := Sub(c.a, c.b); got != c.want {
			t.Errorf("Sub(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
