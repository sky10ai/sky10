package commands

import "testing"

func TestLocalHTTPURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addr    string
		host    string
		want    string
		wantErr bool
	}{
		{
			name: "ipv6 wildcard listener",
			addr: "[::]:9101",
			host: "localhost",
			want: "http://localhost:9101",
		},
		{
			name: "bare port listener",
			addr: ":9101",
			host: "localhost",
			want: "http://localhost:9101",
		},
		{
			name: "ipv4 listener",
			addr: "127.0.0.1:9101",
			host: "localhost",
			want: "http://localhost:9101",
		},
		{
			name: "loopback readiness probe",
			addr: "[::]:9101",
			host: "127.0.0.1",
			want: "http://127.0.0.1:9101",
		},
		{
			name:    "missing host-port separator",
			addr:    "9101",
			host:    "localhost",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := localHTTPURL(tc.addr, tc.host)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("localHTTPURL(%q, %q) error = nil, want non-nil", tc.addr, tc.host)
				}
				return
			}
			if err != nil {
				t.Fatalf("localHTTPURL(%q, %q) error = %v", tc.addr, tc.host, err)
			}
			if got != tc.want {
				t.Fatalf("localHTTPURL(%q, %q) = %q, want %q", tc.addr, tc.host, got, tc.want)
			}
		})
	}
}
