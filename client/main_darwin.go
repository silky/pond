package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	dev := os.Getenv("POND") == "dev"
	runtime.GOMAXPROCS(4)

	home := os.Getenv("HOME")
	if len(home) == 0 {
		fmt.Fprintf(os.Stderr, "$HOME not set. Please either export $HOME or use --state-file to set the location of the state file explicitly.\n")
		os.Exit(1)
	}
	stateFile := filepath.Join(home, ".pond")

	if dev {
		stateFile = "state"
	}

	ui := NewGTKUI()
	client := NewClient(stateFile, ui, rand.Reader, false /* testing */, true /* autoFetch */)
	client.dev = dev
	client.Start()
	ui.Run()
}
