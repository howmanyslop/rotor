package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
)

type buildProfiles struct {
	files     []*os.File
	cpu       bool
	tracing   bool
	blockFile *os.File
	mutexFile *os.File
	heapFile  *os.File
}

func startBuildProfiles(args *buildArgs) (*buildProfiles, error) {
	profiles := &buildProfiles{}
	for _, output := range []struct {
		path        string
		destination **os.File
	}{
		{args.blockprofile, &profiles.blockFile},
		{args.mutexprofile, &profiles.mutexFile},
		{args.heapprofile, &profiles.heapFile},
	} {
		if output.path == "" {
			continue
		}
		file, err := profiles.create(output.path)
		if err != nil {
			profiles.stop()
			return nil, fmt.Errorf("create profile %q: %w", output.path, err)
		}
		*output.destination = file
	}
	if args.blockprofile != "" {
		runtime.SetBlockProfileRate(1_000_000)
	}
	if args.mutexprofile != "" {
		runtime.SetMutexProfileFraction(5)
	}
	if args.cpuprofile != "" {
		file, err := profiles.create(args.cpuprofile)
		if err != nil {
			profiles.stop()
			return nil, fmt.Errorf("create cpu profile: %w", err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			profiles.stop()
			return nil, fmt.Errorf("start cpu profile: %w", err)
		}
		profiles.cpu = true
	}
	if args.traceOut != "" {
		file, err := profiles.create(args.traceOut)
		if err != nil {
			profiles.stop()
			return nil, fmt.Errorf("create trace: %w", err)
		}
		if err := trace.Start(file); err != nil {
			profiles.stop()
			return nil, fmt.Errorf("start trace: %w", err)
		}
		profiles.tracing = true
	}
	return profiles, nil
}

func (profiles *buildProfiles) create(path string) (*os.File, error) {
	file, err := os.Create(path)
	if err == nil {
		profiles.files = append(profiles.files, file)
	}
	return file, err
}

func (profiles *buildProfiles) stop() {
	if profiles == nil {
		return
	}
	if profiles.tracing {
		trace.Stop()
		profiles.tracing = false
	}
	if profiles.cpu {
		pprof.StopCPUProfile()
		profiles.cpu = false
	}
	profiles.writeProfile("block", profiles.blockFile)
	profiles.writeProfile("mutex", profiles.mutexFile)
	profiles.writeProfile("heap", profiles.heapFile)
	for _, file := range profiles.files {
		_ = file.Close()
	}
	profiles.files = nil
	if profiles.blockFile != nil {
		runtime.SetBlockProfileRate(0)
	}
	if profiles.mutexFile != nil {
		runtime.SetMutexProfileFraction(0)
	}
}

func (profiles *buildProfiles) writeProfile(name string, file *os.File) {
	if file == nil {
		return
	}
	if profile := pprof.Lookup(name); profile != nil {
		if err := profile.WriteTo(file, 0); err != nil {
			fmt.Fprintf(os.Stderr, "rotor build: write %s profile: %v\n", name, err)
		}
	}
}
