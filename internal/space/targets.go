package space

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/WildPino/ouroboros/internal/config"
)

// MinPruneHeadroom is the lower bound of prune_headroom (DESIGN.md §3.4).
const MinPruneHeadroom = 1 << 30

// historyWindow is the number of past runs of a job the predictor looks at.
const historyWindow = 10

// Targets are the space limits for one partition, resolved to bytes.
type Targets struct {
	MinFree       uint64
	PruneHeadroom uint64
	MaxRepoSize   uint64 // 0 = no limit
}

// NewTargets resolves the [space] settings for a partition of total bytes.
func NewTargets(s config.Space, total uint64) Targets {
	t := Targets{
		MinFree:       s.MinFree.Resolve(total),
		PruneHeadroom: max(s.PruneHeadroom.Resolve(total), MinPruneHeadroom),
	}
	if s.MaxRepoSize.IsSet() {
		t.MaxRepoSize = s.MaxRepoSize.Resolve(total)
	}
	return t
}

// RequiredFree is the free space needed before a backup expected to add
// growth bytes: growth + min_free + prune_headroom (DESIGN.md §3.4).
func (t Targets) RequiredFree(growth uint64) uint64 {
	return addSat(addSat(growth, t.MinFree), t.PruneHeadroom)
}

// ErrBackupTooLarge means a backup could never fit, even with an empty
// repository; deleting history would not help.
var ErrBackupTooLarge = errors.New("backup is larger than the partition can ever hold")

// CheckFits fails early when growth exceeds what the partition (or
// max_repo_size) can hold at all (DESIGN.md §3.3).
func (t Targets) CheckFits(growth, total uint64) error {
	if need := t.RequiredFree(growth); need > total {
		return fmt.Errorf("%w: needs %d bytes free, partition is %d bytes", ErrBackupTooLarge, need, total)
	}
	if t.MaxRepoSize > 0 && growth > t.MaxRepoSize {
		return fmt.Errorf("%w: expected growth %d bytes exceeds max_repo_size %d bytes", ErrBackupTooLarge, growth, t.MaxRepoSize)
	}
	return nil
}

// PredictGrowth estimates how much the next backup of a job will add to the
// repository from the packed bytes added by its previous runs, oldest first:
// the largest of the last 10 runs times factor (DESIGN.md §3.3). It returns
// false when there is no history.
func PredictGrowth(history []uint64, factor float64) (uint64, bool) {
	if len(history) == 0 {
		return 0, false
	}
	recent := history[max(0, len(history)-historyWindow):]
	return scale(slices.Max(recent), factor), true
}

// FirstRunGrowth scales the dry-run estimate of a job's first backup.
func FirstRunGrowth(estimate uint64, factor float64) uint64 { return scale(estimate, factor) }

func scale(n uint64, factor float64) uint64 {
	v := math.Ceil(float64(n) * factor)
	if v >= math.MaxUint64 {
		return math.MaxUint64
	}
	return uint64(v)
}

func addSat(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}
