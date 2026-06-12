package transport

import "testing"

func TestPortOf(t *testing.T) {
	cases := map[string]string{
		"100.64.0.1:7600": "7600",
		":7600":           "7600",
		"7600":            "7600",
		"host.tail:22":    "22",
	}
	for in, want := range cases {
		if got := portOf(in); got != want {
			t.Errorf("portOf(%q) = %q, want %q", in, got, want)
		}
	}
}
