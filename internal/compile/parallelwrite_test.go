package compile

import "testing"

func TestWriteConcurrency(t *testing.T) {
	tests := []struct {
		name       string
		rbxtsc     string
		rotor      string
		threadpool string
		want       int
	}{
		{name: "default", want: 8},
		{name: "rbxtsc override", rbxtsc: "12", rotor: "3", threadpool: "16", want: 12},
		{name: "threadpool fallback", threadpool: "16", want: 32},
		{name: "invalid override fallback", rbxtsc: "invalid", threadpool: "16", want: 32},
		{name: "hard cap", rbxtsc: "99999", want: 256},
		{name: "legacy alias", rotor: "3", want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RBXTSC_WRITE_CONCURRENCY", tt.rbxtsc)
			t.Setenv("ROTOR_WRITE_WORKERS", tt.rotor)
			t.Setenv("UV_THREADPOOL_SIZE", tt.threadpool)
			if got := writeWorkers(); got != tt.want {
				t.Fatalf("writeWorkers() = %d, want %d", got, tt.want)
			}
		})
	}
}
