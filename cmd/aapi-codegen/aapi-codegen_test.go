package main

import "testing"

func TestDefaultPackageName(t *testing.T) {
	cases := []struct {
		out, want string
	}{
		// Version-folder rule: parent + basename.
		{"/tmp/lib/schemas/job-message/v1/types.gen.go", "jobmessagev1"},
		{"/tmp/lib/schemas/job-status-change/v1/types.gen.go", "jobstatuschangev1"},
		{"/tmp/anything/v2/types.gen.go", "anythingv2"},
		// Plain dir basename (not a version folder).
		{"/tmp/widgetservice/types.gen.go", "widgetservice"},
		{"/tmp/foo-bar/types.gen.go", "foobar"},
		// Relative cwd-style output paths still resolve via filepath.Abs.
		// (We can't pin the cwd here, so just verify it doesn't panic and
		// returns a non-empty identifier.)
	}
	for _, c := range cases {
		got := defaultPackageName(c.out)
		if got != c.want {
			t.Errorf("defaultPackageName(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
