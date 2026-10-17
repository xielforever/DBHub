package queryapi

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func processResources() (*float64, *uint64) {
	var cpu *float64
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		seconds := float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
		cpu = &seconds
	}
	var rss *uint64
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				bytes := pages * uint64(os.Getpagesize())
				rss = &bytes
			}
		}
	}
	return cpu, rss
}
