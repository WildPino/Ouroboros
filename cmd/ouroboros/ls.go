package main

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend"
	"github.com/WildPino/ouroboros/internal/backend/restic"
)

func newLsCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "ls SNAPSHOT [FOLDER]",
		Short: "List one folder of a snapshot",
		Long: "List the files and folders directly inside FOLDER in a snapshot, or the drives\n" +
			`when FOLDER is omitted. FOLDER is a Windows path such as C:\Users\me; its letter` + "\n" +
			"case must match the one stored in the snapshot.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := ""
			if len(args) == 2 {
				folder = args[1]
			}
			return runLs(cmd.Context(), opts, defaultRepoDeps(), args[0], folder)
		},
	})
}

func runLs(ctx context.Context, opts *options, d repoDeps, ref, folder string) error {
	s, err := openRepo(ctx, opts, d, "ls", false)
	if err != nil {
		return err
	}
	defer s.release()
	snaps, err := s.backend.Snapshots(ctx, backend.SnapshotFilter{})
	if err != nil {
		return err
	}
	sn, err := resolveSnapshot(snaps, ref)
	if err != nil {
		return err
	}
	dir := "/"
	if folder != "" {
		dir = restic.SnapshotPath(folder)
	}
	nodes, err := s.backend.List(ctx, sn.ID, dir)
	if err != nil {
		return err
	}
	if opts.out != nil {
		opts.out.result(lsJSON(sn, dir, nodes))
		return nil
	}
	tw := tabwriter.NewWriter(opts.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Type\tSize\tModified\tName")
	for _, n := range nodes {
		size := ""
		if n.Type == "file" {
			size = formatBytes(n.Size)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n.Type, size, n.ModTime.Local().Format(time.DateTime), n.Name)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(opts.stdout, "%d entries in %s\n", len(nodes), restic.WindowsPath(dir))
	return nil
}

// Schema of "ls --json"; documented in docs/JSON.md.
type lsOut struct {
	SnapshotID string    `json:"snapshot_id"`
	Folder     string    `json:"folder"`
	Entries    []nodeOut `json:"entries"`
}

type nodeOut struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Path      string    `json:"path"`
	SizeBytes *uint64   `json:"size_bytes"`
	Modified  time.Time `json:"modified"`
}

func lsJSON(sn backend.Snapshot, dir string, nodes []backend.Node) lsOut {
	out := lsOut{SnapshotID: sn.ID, Folder: restic.WindowsPath(dir), Entries: []nodeOut{}}
	if dir == "/" {
		out.Folder = ""
	}
	for _, n := range nodes {
		o := nodeOut{Name: n.Name, Type: n.Type, Path: restic.WindowsPath(n.Path), Modified: n.ModTime.UTC()}
		if n.Type == "file" {
			o.SizeBytes = ptr(n.Size)
		}
		out.Entries = append(out.Entries, o)
	}
	return out
}
