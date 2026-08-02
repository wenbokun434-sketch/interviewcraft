//go:build !linux

package main

import "os"

func peakMemoryKB(*os.ProcessState) int64 {
	return 0
}

func killedForMemory(state *os.ProcessState) bool {
	return state != nil && state.ExitCode() == 137
}
