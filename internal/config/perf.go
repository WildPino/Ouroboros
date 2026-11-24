package config

// Pack sizes in MiB per memory profile (DESIGN.md §7).
const (
	packSizeLow  = 16
	packSizeAuto = 32
	packSizeHigh = 64
)

// lowMemory is the free RAM below which the auto profile behaves as low.
const lowMemory = 1 << 30

// Resolve returns the restic read concurrency and pack size (MiB) for this
// machine: the memory profile adjusted to cpus and freeRAM, with explicit
// read_concurrency and pack_size_mb taking precedence.
//
// The auto profile reads with half the CPUs, between 2 and 8, and keeps the
// packs restic may hold in memory (about two per reader) within a quarter
// of the free RAM, lowering first the concurrency and then the pack size.
// freeRAM 0 means unknown and is treated as plenty.
func (p Performance) Resolve(cpus int, freeRAM uint64) (readConcurrency, packSizeMB int) {
	cpus = max(cpus, 1)
	switch p.MemoryProfile {
	case MemoryProfileLow:
		readConcurrency, packSizeMB = 2, packSizeLow
	case MemoryProfileHigh:
		readConcurrency, packSizeMB = min(16, cpus), packSizeHigh
	default:
		readConcurrency, packSizeMB = autoProfile(cpus, freeRAM)
	}
	if p.ReadConcurrency > 0 {
		readConcurrency = p.ReadConcurrency
	}
	if p.PackSizeMB > 0 {
		packSizeMB = p.PackSizeMB
	}
	return readConcurrency, packSizeMB
}

func autoProfile(cpus int, freeRAM uint64) (int, int) {
	rc, pack := min(max(cpus/2, 2), 8), packSizeAuto
	if freeRAM == 0 {
		return rc, pack
	}
	if freeRAM < lowMemory {
		return 2, packSizeLow
	}
	budget := freeRAM / 4
	need := func() uint64 { return uint64(rc) * 2 * uint64(pack) << 20 }
	for rc > 2 && need() > budget {
		rc--
	}
	if need() > budget {
		pack = packSizeLow
	}
	return rc, pack
}
