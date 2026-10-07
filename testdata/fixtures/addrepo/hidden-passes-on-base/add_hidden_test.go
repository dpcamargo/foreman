package addrepo

import "testing"

func TestAddHidden(t *testing.T) {
	if Add(2, 2) != 4 {
		t.Fatal("Add(2, 2) != 4")
	}
}
