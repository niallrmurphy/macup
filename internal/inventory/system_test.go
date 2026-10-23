package inventory

import "testing"

func TestIsSystemManagedApp(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/System/Applications/Chess.app", true},
		{"/System/Applications/Utilities/Terminal.app", true},
		{"/System/Library/CoreServices/Applications/Archive Utility.app", true},
		{"/System/ApplicationsExtra/Example.app", false},
		{"/Applications/Chess.app", false},
		{"/Applications/Keynote.app", false},
		{"/Users/example/Applications/Foo.app", false},
		{"System/Applications/Chess.app", false},
		{"/Applications/../System/Applications/Chess.app", true},
	}
	for _, tt := range tests {
		if got := IsSystemManagedApp(tt.path); got != tt.want {
			t.Errorf("%q: got %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFilterSystemApplicationsPreservesOrder(t *testing.T) {
	apps := []Application{
		{Name: "Chess", Path: "/System/Applications/Chess.app"},
		{Name: "BBEdit", Path: "/Applications/BBEdit.app"},
		{Name: "Terminal", Path: "/System/Applications/Utilities/Terminal.app"},
		{Name: "Pages", Path: "/Applications/Pages.app"},
	}
	got := FilterSystemApplications(apps)
	if len(got) != 2 || got[0].Name != "BBEdit" || got[1].Name != "Pages" {
		t.Fatalf("filtered: %+v", got)
	}
	if len(apps) != 4 {
		t.Fatal("mutated original slice")
	}
}
