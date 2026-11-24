package space

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// KeepRules is the optional time-based retention (DESIGN.md §3.7).
type KeepRules struct {
	Hourly, Daily, Weekly, Monthly, Yearly int
}

// Enabled reports whether any rule is set.
func (k KeepRules) Enabled() bool {
	return k.Hourly+k.Daily+k.Weekly+k.Monthly+k.Yearly > 0
}

type keepRule struct {
	count  int
	bucket func(time.Time) int
}

func (k KeepRules) rules() []keepRule {
	return []keepRule{
		{k.Hourly, func(t time.Time) int { y, m, d := t.Date(); return y*1000000 + int(m)*10000 + d*100 + t.Hour() }},
		{k.Daily, func(t time.Time) int { y, m, d := t.Date(); return y*10000 + int(m)*100 + d }},
		{k.Weekly, func(t time.Time) int { y, w := t.ISOWeek(); return y*100 + w }},
		{k.Monthly, func(t time.Time) int { y, m, _ := t.Date(); return y*100 + int(m) }},
		{k.Yearly, func(t time.Time) int { return t.Year() }},
	}
}

// RetentionKeep returns the IDs that the rules keep, with the semantics of
// restic forget --keep-hourly/daily/weekly/monthly/yearly applied to each
// job separately: walking from the newest snapshot, a rule keeps a snapshot
// when it opens a new hour/day/week/month/year, until the rule has kept its
// count. Times are taken in loc.
func RetentionKeep(snaps []backend.Snapshot, k KeepRules, loc *time.Location) map[string]bool {
	keep := map[string]bool{}
	byJob := map[string][]backend.Snapshot{}
	for _, s := range snaps {
		byJob[s.Job()] = append(byJob[s.Job()], s)
	}
	for _, group := range byJob {
		slices.SortFunc(group, func(a, b backend.Snapshot) int { return oldestFirst(b, a) })
		rules := k.rules()
		last := make([]int, len(rules))
		for i := range last {
			last[i] = -1
		}
		for _, s := range group {
			t := s.Time.In(loc)
			for i := range rules {
				if rules[i].count <= 0 {
					continue
				}
				if b := rules[i].bucket(t); b != last[i] {
					last[i] = b
					rules[i].count--
					keep[s.ID] = true
				}
			}
		}
	}
	return keep
}

// forgetChunk bounds the IDs passed to one restic call, to stay well below
// the Windows command line limit.
const forgetChunk = 100

// ApplyRetention forgets the snapshots that the time-based rules do not
// keep. Protected snapshots (newest per job, recent, pinned) are never
// forgotten, whatever the rules say. The data is freed by the next prune;
// a following Reclaim prunes first so that it does not delete more history
// than needed.
func (r *Reclaimer) ApplyRetention(ctx context.Context, k KeepRules, loc *time.Location) (Report, error) {
	var rep Report
	if !k.Enabled() {
		return rep, nil
	}
	snaps, err := r.Repo.Snapshots(ctx, backend.SnapshotFilter{Host: r.Host, Tags: []string{backend.TagOuroboros}})
	if err != nil {
		return rep, fmt.Errorf("list snapshots: %w", err)
	}
	keep := RetentionKeep(snaps, k, loc)
	var victims []backend.Snapshot
	for _, c := range SelectCandidates(snaps, r.Policy, r.Clock.Now()).Candidates {
		if !keep[c.ID] {
			victims = append(victims, c)
		}
	}
	for chunk := range slices.Chunk(victims, forgetChunk) {
		ids := make([]string, len(chunk))
		for i, s := range chunk {
			ids[i] = s.ID
		}
		if err := r.Repo.Forget(ctx, ids); err != nil {
			return rep, fmt.Errorf("forget: %w", err)
		}
		rep.Forgotten = append(rep.Forgotten, chunk...)
		r.prunePending = true
	}
	if len(victims) > 0 {
		r.log().Info("retention forgot snapshots", "count", len(victims))
	}
	return rep, nil
}
