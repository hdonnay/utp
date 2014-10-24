package utp

import "testing"

func TestMinU16(t *testing.T) {
	if minU16(1, 1) != 1 {
		t.Fatal("(1, 1) != 1")
	}
	if minU16(0, 1) != 0 {
		t.Fatal("(0, 1) != 0")
	}
	if minU16(1, 0) != 0 {
		t.Fatal("(1, 0) != 0")
	}
}
