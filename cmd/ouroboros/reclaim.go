package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/space"
)

func newReclaimCmd(opts *options) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reclaim --dry-run",
		Short: "Show which snapshots would be deleted to reach the target free space",
		Long: "Show the free space missing on the repository partition and the order in which\n" +
			"snapshots would be deleted, oldest first, without changing anything.\n" +
			"Space is reclaimed automatically before each backup; this command only plans.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dryRun {
				return errors.New("reclaim only supports --dry-run; space is reclaimed automatically before each backup")
			}
			cfg, err := config.Load(opts.configPath)
			if err != nil {
				return err
			}
			b, err := newResticBackend(cfg, opts.configPath)
			if err != nil {
				return err
			}
			host, err := os.Hostname()
			if err != nil {
				return err
			}
			r, err := newReclaimer(cfg, b, host)
			if err != nil {
				return err
			}
			plan, err := r.Plan(cmd.Context(), 0)
			if err != nil {
				return err
			}
			writePlan(opts.stdout, plan, r.Targets)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only show what would be deleted")
	return cmd
}

// newReclaimer wires the space manager to the real disk for cfg. host must
// be the hostname the backend records in snapshots.
func newReclaimer(cfg *config.Config, repo space.Repository, host string) (*space.Reclaimer, error) {
	disk := space.SystemDisk{}
	u, err := disk.Usage(cfg.Repository.Path)
	if err != nil {
		return nil, fmt.Errorf("measure repository partition: %w", err)
	}
	return &space.Reclaimer{
		Repo:     repo,
		Disk:     disk,
		Sizer:    space.DirSizer{},
		Clock:    space.SystemClock{},
		RepoPath: cfg.Repository.Path,
		Host:     host,
		Policy: space.Policy{
			KeepMin:          cfg.Space.KeepMinSnapshots,
			ProtectNewerThan: cfg.Space.ProtectNewerThan.Std(),
		},
		Targets:     space.NewTargets(cfg.Space, u.Total),
		Ballast:     space.BallastFile{Path: space.BallastPath(cfg.Repository.Path)},
		BallastSize: cfg.Space.ReserveFileSize.Resolve(0),
	}, nil
}

func writePlan(w io.Writer, p space.PlanReport, t space.Targets) {
	fmt.Fprintf(w, "Partition:  %s, %s free\n", formatBytes(p.Usage.Total), formatBytes(p.Usage.Free))
	fmt.Fprintf(w, "Target:     %s free (min_free %s + prune headroom %s)\n",
		formatBytes(p.NeedFree), formatBytes(t.MinFree), formatBytes(t.PruneHeadroom))
	if t.MaxRepoSize > 0 {
		fmt.Fprintf(w, "Repository: %s of max_repo_size %s\n", formatBytes(p.RepoSize), formatBytes(t.MaxRepoSize))
	}

	sel := p.Selection
	if p.Missing == 0 && !p.OverMaxRepoSize {
		fmt.Fprintln(w, "\nNothing to reclaim: the target is already met.")
	} else {
		if p.Missing > 0 {
			fmt.Fprintf(w, "Missing:    %s\n", formatBytes(p.Missing))
		}
		if p.OverMaxRepoSize {
			fmt.Fprintln(w, "The repository exceeds max_repo_size.")
		}
		if len(sel.Candidates) == 0 {
			fmt.Fprintln(w, "\nNo snapshot can be deleted: all of them are protected.")
		} else {
			fmt.Fprintln(w, "\nDeletion order (oldest first; deletion stops as soon as the target is met,")
			fmt.Fprintln(w, "and the space each snapshot frees is only known after pruning):")
			for i, s := range sel.Candidates {
				fmt.Fprintf(w, "  %3d. %s  %-16s %s\n", i+1, s.Time.Local().Format(time.DateTime), s.Job(), shortID(s.ID))
			}
		}
	}

	if len(sel.Protected) > 0 {
		counts := map[string]int{}
		for _, pr := range sel.Protected {
			counts[pr.Reason]++
		}
		fmt.Fprintf(w, "\nProtected:  %d snapshots (%d newest of their job, %d recent, %d pinned)\n",
			len(sel.Protected), counts[space.ReasonNewest], counts[space.ReasonRecent], counts[space.ReasonPinned])
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
