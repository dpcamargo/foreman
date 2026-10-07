package ambiguous

import "testing"

func TestAddIsProduct(t *testing.T) {
	if got := Add(2, 3); got != 6 {
		t.Fatalf("Add(2, 3) = %d, want 6", got)
	}
}
