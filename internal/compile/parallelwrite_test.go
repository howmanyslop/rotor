package compile

import "testing"

func TestWriteWorkers(t *testing.T) {
	tests := []struct {
		name       string
		rbxtsc     string
		rotor      string
		threadpool string
		want       int
	}{
		{name: "rbxtsc override wins", rbxtsc: "12", rotor: "3", threadpool: "16", want: 12},
		{name: "rotor override wins over threadpool", rotor: "3", threadpool: "16", want: 3},
		{name: "threadpool does not select workers", threadpool: "16", want: 8},
		{name: "default", want: 8},
		{name: "explicit cap", rbxtsc: "256", want: 256},
		{name: "over cap", rbxtsc: "99999", want: 256},
		{name: "floors valid override", rbxtsc: "3.9", want: 3},
		{name: "invalid rbxtsc falls through to rotor", rbxtsc: "invalid", rotor: "3", want: 3},
		{name: "invalid rbxtsc falls through to default", rbxtsc: "invalid", threadpool: "16", want: 8},
		{name: "zero rbxtsc falls through to default", rbxtsc: "0", threadpool: "16", want: 8},
		{name: "negative rbxtsc falls through to default", rbxtsc: "-1", threadpool: "16", want: 8},
		{name: "nan rbxtsc falls through to default", rbxtsc: "NaN", threadpool: "16", want: 8},
		{name: "infinite rbxtsc falls through to default", rbxtsc: "Inf", threadpool: "16", want: 8},
		{name: "invalid rotor falls through to default", rotor: "invalid", threadpool: "16", want: 8},
		{name: "zero rotor falls through to default", rotor: "0", threadpool: "16", want: 8},
		{name: "negative rotor falls through to default", rotor: "-1", threadpool: "16", want: 8},
		{name: "nan rotor falls through to default", rotor: "NaN", threadpool: "16", want: 8},
		{name: "infinite rotor falls through to default", rotor: "Inf", threadpool: "16", want: 8},
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
