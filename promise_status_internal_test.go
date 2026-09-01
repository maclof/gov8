//go:build windows && amd64

package gov8

import "testing"

func TestMapPromiseReactionError(t *testing.T) {
	tests := []struct {
		name          string
		input         *ShimError
		wantSame      bool
		wantPlainType bool
		wantException bool
	}{
		{
			name:          "handler type",
			input:         &ShimError{Op: "Promise.Then", Code: errBadArg, Detail: promiseHandlerTypeErrorDetail},
			wantPlainType: true,
		},
		{
			name:     "other bad argument",
			input:    &ShimError{Op: "Promise.Then", Code: errBadArg, Detail: "invalid argument"},
			wantSame: true,
		},
		{
			name:          "same detail wrong status",
			input:         &ShimError{Op: "Promise.Then", Code: errCpp, Detail: promiseHandlerTypeErrorDetail},
			wantSame:      true,
			wantException: false,
		},
		{
			name:          "empty maybe exception",
			input:         &ShimError{Op: "Promise.Then2", Code: errException, Detail: "Promise::Then2 failed"},
			wantSame:      true,
			wantException: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mapPromiseReactionError(test.input)
			if test.wantSame && got != test.input {
				t.Fatalf("mapped error pointer changed: got %v, want original", got)
			}
			if !test.wantSame && got == test.input {
				t.Fatalf("mapped error pointer unchanged: %v", got)
			}
			if test.wantPlainType {
				if got.Error() != promiseHandlerTypeError {
					t.Fatalf("mapped error = %q, want %q", got, promiseHandlerTypeError)
				}
				if _, ok := got.(*ShimError); ok {
					t.Fatalf("handler type error remained ShimError: %v", got)
				}
			}
			if IsException(got) != test.wantException {
				t.Fatalf("IsException(%v) = %v, want %v", got, IsException(got), test.wantException)
			}
		})
	}
}
