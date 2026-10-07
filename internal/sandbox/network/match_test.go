package network

import "testing"

func TestMatchesDestination(t *testing.T) {
	cases := []struct {
		rule, host string
		want       bool
	}{
		{"*", "api.example.com", true},
		{"api.example.com", "api.example.com", true},
		{".example.com", "api.example.com", true},
		{".example.com", "example.com", true},
		{".example.com", "notexample.com", false},
		{"10.0.0.0/8", "10.1.2.3", true},
		{"10.0.0.0/8", "11.1.2.3", false},
		{"10.0.0.0/8", "api.example.com", false},
	}
	for _, c := range cases {
		if got := MatchesDestination(c.rule, c.host); got != c.want {
			t.Errorf("MatchesDestination(%q, %q) = %v; want %v", c.rule, c.host, got, c.want)
		}
	}
}
