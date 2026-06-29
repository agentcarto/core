package common

import "testing"

func TestText(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		// A missing content field (nil) becomes an empty string, never "null"
		// (regression guard against polluting THINK lines).
		{"nil", nil, ""},
		{"string", "hi", "hi"},
		{"blocks", []any{
			map[string]any{"text": "a"},
			map[string]any{"text": "b"},
		}, "a\nb"},
		{"empty slice", []any{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in); got != c.want {
				t.Errorf("Text(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
