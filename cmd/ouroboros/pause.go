package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/platform/windows"
)

var errTaskNotInstalled = errors.New(`the scheduled task is not installed (run "ouroboros install")`)

func newPauseCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "pause",
		Short: "Pause automatic backups",
		Long: "Disable the scheduled task " + windows.TaskName + ", so that no backup or maintenance\n" +
			"starts on its own until \"ouroboros resume\". A run already in progress finishes.\n" +
			"\"ouroboros run\" still works. Must be run as Administrator.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPause(cmd.Context(), opts, defaultInstallDeps(), true)
		},
	})
}

func newResumeCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "resume",
		Short: "Resume automatic backups",
		Long: "Enable the scheduled task again after \"ouroboros pause\". Backups missed while\n" +
			"paused run at the next tick. Must be run as Administrator.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPause(cmd.Context(), opts, defaultInstallDeps(), false)
		},
	})
}

// pauseOut is the result of "pause --json" and "resume --json".
type pauseOut struct {
	Paused bool `json:"paused"`
}

func runPause(ctx context.Context, opts *options, d installDeps, pause bool) error {
	what := "resume"
	if pause {
		what = "pause"
	}
	if err := requireElevation(d, what); err != nil {
		return err
	}
	st, err := d.tasks.State(ctx, windows.TaskName)
	if err != nil {
		return err
	}
	switch {
	case st == windows.TaskMissing:
		return errTaskNotInstalled
	case pause && st == windows.TaskDisabled:
		fmt.Fprintln(opts.stderr, "Automatic backups were already paused.")
	case !pause && st == windows.TaskEnabled:
		fmt.Fprintln(opts.stderr, "Automatic backups were already on.")
	default:
		if err := d.tasks.SetEnabled(ctx, windows.TaskName, !pause); err != nil {
			return err
		}
	}
	if opts.out != nil {
		opts.out.result(pauseOut{Paused: pause})
		return nil
	}
	if pause {
		fmt.Fprintln(opts.stdout, `Automatic backups are paused; "ouroboros resume" turns them back on.`)
	} else {
		fmt.Fprintln(opts.stdout, "Automatic backups are on.")
	}
	return nil
}
