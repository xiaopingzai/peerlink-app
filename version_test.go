package main

import "testing"

func TestVersionMatrix(t *testing.T) {
	cases := []struct {
		remote, local string
		want          bool
	}{
		{"1.0.2-2", "1.0.2", true},
		{"1.0.2-2", "1.0.2-1", true},
		{"1.0.2-2", "1.0.2-2", false},
		{"1.0.2", "1.0.2-2", false},
		{"1.0.3", "1.0.2-2", true},
	}
	for _, c := range cases {
		got := versionNewer(c.remote, c.local)
		if got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v want %v", c.remote, c.local, got, c.want)
		}
	}
}
