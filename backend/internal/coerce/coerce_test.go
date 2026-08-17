package coerce

import (
	"encoding/json"
	"testing"
)

func TestInt(t *testing.T) {
	cases := []struct {
		value any
		want  int
	}{
		{int64(7), 7},
		{7, 7},
		{7.9, 7},
		{json.Number("7"), 7},
		{"7", 7},
		{" 7 ", 7},
		{[]byte("7"), 7},
		{"seven", 0},
		{nil, 0},
	}
	for _, testCase := range cases {
		if got := Int(testCase.value); got != testCase.want {
			t.Errorf("Int(%#v) = %d, want %d", testCase.value, got, testCase.want)
		}
	}
}

func TestInt64ReportsFailure(t *testing.T) {
	if _, ok := Int64("nope"); ok {
		t.Error("Int64 accepted a non-numeric string")
	}
	if number, ok := Int64(json.Number("42")); !ok || number != 42 {
		t.Errorf("Int64(json.Number) = %d, %v", number, ok)
	}
}

func TestText(t *testing.T) {
	if got := Text("  hi  "); got != "hi" {
		t.Errorf("Text = %q", got)
	}
	if got := Text(42); got != "" {
		t.Errorf("Text(42) = %q, want empty", got)
	}
}

func TestNumberText(t *testing.T) {
	cases := map[any]string{
		"abc":              "abc",
		float64(12):        "12",
		json.Number("345"): "345",
		nil:                "",
	}
	for value, want := range cases {
		if got := NumberText(value); got != want {
			t.Errorf("NumberText(%#v) = %q, want %q", value, got, want)
		}
	}
}

func TestStringOr(t *testing.T) {
	if got := StringOr(nil, "fallback"); got != "fallback" {
		t.Errorf("StringOr(nil) = %q", got)
	}
	if got := StringOr("", "fallback"); got != "fallback" {
		t.Errorf("StringOr(\"\") = %q", got)
	}
	if got := StringOr(int64(5), "fallback"); got != "5" {
		t.Errorf("StringOr(5) = %q", got)
	}
}
