package config

import "testing"

func TestPerformanceResolve(t *testing.T) {
	const GB = 1 << 30
	tests := []struct {
		name       string
		perf       Performance
		cpus       int
		free       uint64
		rc, packMB int
	}{
		{"low", Performance{MemoryProfile: MemoryProfileLow}, 32, 64 * GB, 2, 16},
		{"high, many CPUs", Performance{MemoryProfile: MemoryProfileHigh}, 32, 64 * GB, 16, 64},
		{"high, few CPUs", Performance{MemoryProfile: MemoryProfileHigh}, 4, 64 * GB, 4, 64},
		{"auto, plenty of RAM", Performance{MemoryProfile: MemoryProfileAuto}, 12, 16 * GB, 6, 32},
		{"auto, capped at 8", Performance{MemoryProfile: MemoryProfileAuto}, 64, 64 * GB, 8, 32},
		{"auto, at least 2", Performance{MemoryProfile: MemoryProfileAuto}, 1, 16 * GB, 2, 32},
		{"auto, RAM unknown", Performance{MemoryProfile: MemoryProfileAuto}, 12, 0, 6, 32},
		// 1.5 GB free: budget 384 MiB fits 6 readers × 2 × 32 MiB.
		{"auto, limited RAM", Performance{MemoryProfile: MemoryProfileAuto}, 16, 3 * GB / 2, 6, 32},
		{"auto, little RAM", Performance{MemoryProfile: MemoryProfileAuto}, 16, GB / 2, 2, 16},
		{"empty profile is auto", Performance{}, 8, 16 * GB, 4, 32},
		{"explicit overrides", Performance{MemoryProfile: MemoryProfileLow, ReadConcurrency: 6, PackSizeMB: 48}, 2, GB, 6, 48},
		{"explicit concurrency only", Performance{MemoryProfile: MemoryProfileHigh, ReadConcurrency: 3}, 32, 64 * GB, 3, 64},
	}
	for _, tt := range tests {
		rc, pack := tt.perf.Resolve(tt.cpus, tt.free)
		if rc != tt.rc || pack != tt.packMB {
			t.Errorf("%s: got %d readers, %d MiB; want %d, %d", tt.name, rc, pack, tt.rc, tt.packMB)
		}
	}
}
