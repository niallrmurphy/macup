package main

import "testing"

func TestProviders(t *testing.T) {
	for _, tc := range []struct {
		input      string
		m, s, fail bool
	}{
		{"all", true, true, false}, {"none", false, false, false}, {"microsoft", true, false, false}, {"sparkle", false, true, false}, {"microsoft,sparkle", true, true, false}, {"bogus", false, false, true}, {"none,microsoft", false, false, true},
	} {
		p, err := parseProviders(tc.input)
		if (err != nil) != tc.fail {
			t.Fatalf("%s err %v", tc.input, err)
		}
		if err == nil && (p["microsoft"] != tc.m || p["sparkle"] != tc.s) {
			t.Fatalf("%s: %v", tc.input, p)
		}
	}
}
