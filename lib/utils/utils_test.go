package utils

import (
	"testing"
)

func TestRandomString(t *testing.T) {
	tests := []struct {
		length         int
		expectedLength int
	}{
		{0, 0},
		{1, 1},
		{10, 10},
		{100, 100},
		{1000, 1000},
	}

	for _, test := range tests {
		result := RandomString(test.length)
		if len(result) != test.expectedLength {
			t.Errorf("RandomString(%d) = %s; want length %d", test.length, result, test.expectedLength)
		}

		if test.length > 1 {
			resultTwo := RandomString(test.length)
			if len(resultTwo) != test.expectedLength {
				t.Errorf("Second iteration: RandomString(%d) = %s; want length %d", test.length, result, test.expectedLength)
			}

			if result == resultTwo {
				t.Errorf("RandomString(%d) = %s; RandomString(%d) = %s; want different strings", test.length, result, test.length, resultTwo)
			}
		}
	}
}
