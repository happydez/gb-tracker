package events

import "testing"

func TestErrorType(t *testing.T) {
	got := ErrorType(SourceTracker)
	want := "errors.tracker"
	if got != want {
		t.Errorf("ErrorType(%q) = %q, want %q", SourceTracker, got, want)
	}
}

func TestIsErrorType(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{typ: ErrorType(SourceTracker), want: true},
		{typ: ErrorType("some-external-plugin"), want: true},
		{typ: TypeModDiscovered, want: false},
		{typ: "", want: false},
		{typ: "errorsomething", want: false},
	}

	for _, tt := range tests {
		if got := IsErrorType(tt.typ); got != tt.want {
			t.Errorf("IsErrorType(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}
