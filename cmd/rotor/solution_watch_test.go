package main

import (
	"reflect"
	"testing"
)

func TestCompileGate(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	transcript := []string{}
	gate := newCompileGate(func() {
		transcript = append(transcript, "start")
		started <- struct{}{}
		<-release
		transcript = append(transcript, "end")
	})

	gate.Trigger()
	<-started
	gate.Trigger()
	gate.Trigger()
	release <- struct{}{}
	<-started
	release <- struct{}{}
	gate.Drain()

	if want := []string{"start", "end", "start", "end"}; !reflect.DeepEqual(transcript, want) {
		t.Fatalf("gate transcript = %v, want %v", transcript, want)
	}
}
