package git

import (
	"context"
	"errors"
	"testing"
)

func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stderr string
		want   bool
	}{
		{
			name:   "nil error and empty stderr",
			err:    nil,
			stderr: "",
			want:   false,
		},
		{
			name:   "nil error with unrelated stderr",
			err:    nil,
			stderr: "CONFLICT (content): Merge conflict in foo.txt",
			want:   false,
		},
		{
			name:   "non-network git error",
			err:    errors.New("some git error"),
			stderr: "CONFLICT (content): Merge conflict in foo.txt",
			want:   false,
		},
		{
			name:   "could not resolve host",
			err:    errors.New("remote error"),
			stderr: "fatal: Could not resolve host: github.com",
			want:   true,
		},
		{
			name:   "could not resolve host case insensitive",
			err:    errors.New("remote error"),
			stderr: "FATAL: COULD NOT RESOLVE HOST: github.com",
			want:   true,
		},
		{
			name:   "connection refused",
			err:    errors.New("remote error"),
			stderr: "ssh: connect to host github.com port 22: Connection refused",
			want:   true,
		},
		{
			name:   "connection timed out",
			err:    errors.New("remote error"),
			stderr: "ssh: Connection timed out",
			want:   true,
		},
		{
			name:   "network is unreachable",
			err:    errors.New("remote error"),
			stderr: "Network is unreachable",
			want:   true,
		},
		{
			name:   "operation timed out",
			err:    errors.New("remote error"),
			stderr: "Operation timed out after 30000 ms",
			want:   true,
		},
		{
			name:   "unable to access",
			err:    errors.New("remote error"),
			stderr: "fatal: unable to access 'https://github.com/foo/bar.git/'",
			want:   true,
		},
		{
			name:   "could not read from remote repository",
			err:    errors.New("remote error"),
			stderr: "fatal: Could not read from remote repository.",
			want:   true,
		},
		{
			name:   "failed to connect",
			err:    errors.New("remote error"),
			stderr: "Failed to connect to github.com port 443",
			want:   true,
		},
		{
			name:   "ssl connect error",
			err:    errors.New("remote error"),
			stderr: "SSL connect error",
			want:   true,
		},
		{
			name:   "temporary failure in name resolution",
			err:    errors.New("remote error"),
			stderr: "Temporary failure in name resolution",
			want:   true,
		},
		{
			name: "context deadline exceeded",
			err: func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				defer cancel()
				return ctx.Err()
			}(),
			stderr: "",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNetworkError(tt.err, tt.stderr)
			if got != tt.want {
				t.Errorf("IsNetworkError() = %v, want %v", got, tt.want)
			}
		})
	}
}
