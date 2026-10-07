package fizzrepo

import "testing"

func TestFizzBuzz(t *testing.T) {
	cases := map[int]string{1: "1", 3: "Fizz", 5: "Buzz", 15: "FizzBuzz", 30: "FizzBuzz", 7: "7", 9: "Fizz", 10: "Buzz"}
	for n, want := range cases {
		if got := FizzBuzz(n); got != want {
			t.Errorf("FizzBuzz(%d) = %q, want %q", n, got, want)
		}
	}
}
