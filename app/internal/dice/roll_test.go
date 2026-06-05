package dice

import "testing"

func TestRoll(t *testing.T) {
	for range 1000 {
		got := Roll()
		if got < 1 || got > 6 {
			t.Fatalf("Roll() = %d, want value in [1, 6]", got)
		}
	}
}
