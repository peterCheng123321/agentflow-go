//go:build darwin && arm64

package io

import (
	"os/exec"
	"strconv"
	"strings"
)

// isAppleSilicon detects if we're running on Apple Silicon.
// On darwin/arm64, this is always true.
func isAppleSilicon() bool {
	return true
}

// supportsAMX detects Apple Matrix Extensions (M2/M3/M4 chips).
func supportsAMX() bool {
	// Check sysctl for hw.perflevel0.physicalcpu
	// M2/M3/M4 have different CPU topology
	data, err := exec.Command("sysctl", "hw.perflevel0.physicalcpu").Output()
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			count, _ := strconv.Atoi(fields[1])
			// M2/M3/M4 typically have 2-8 performance cores with AMX
			return count >= 2
		}
	}
	return false
}

// detectChipInfo fills in detailed chip information.
func detectChipInfo(info *AppleMInfo) {
	// Get performance core count
	if data, err := exec.Command("sysctl", "-n", "hw.perflevel0.physicalcpu").Output(); err == nil {
		if cores, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			info.Cores = cores
		}
	}

	// Get efficiency core count
	if data, err := exec.Command("sysctl", "-n", "hw.perflevel1.physicalcpu").Output(); err == nil {
		if cores, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			info.EfficiencyCores = cores
		}
	}

	// Get memory size
	if data, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		if memBytes, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
			info.MemoryGB = int(memBytes / (1024 * 1024 * 1024))
		}
	}

	// Detect chip model
	info.Model = detectChipModel()
}

// detectChipModel identifies the specific Apple M-series chip.
func detectChipModel() string {
	// Try to detect from hw.optional.arm.FEAT_AMX_V1
	if data, err := exec.Command("sysctl", "-n", "hw.optional.arm.FEAT_AMX_V1").Output(); err == nil {
		if strings.TrimSpace(string(data)) == "1" {
			return "M2/M3/M4" // Has AMX
		}
	}

	// Fall back to checking core counts for model inference
	// M1: 8 cores (4P+4E) or 7 cores (4P+3E) for base/Pro
	// M2: similar, but with AMX
	return "M1/M2/M3/M4" // Generic Apple Silicon
}
