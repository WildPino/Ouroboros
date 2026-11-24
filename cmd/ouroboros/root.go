package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros"
	"github.com/WildPino/ouroboros/internal/config"
)

// options holds flags shared by all subcommands.
type options struct {
	configPath string
	stdout     io.Writer
	stderr     io.Writer
}

func defaultConfigPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "Ouroboros", "ouroboros.toml")
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	opts := &options{stdout: stdout, stderr: stderr}
	root := &cobra.Command{
		Use:           "ouroboros",
		Short:         "Space-aware, set-and-forget versioned backups for Windows",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&opts.configPath, "config", defaultConfigPath(), "path to the configuration file")

	root.AddCommand(
		newVersionCmd(opts),
		newConfigCmd(opts),
		newDoctorCmd(opts),
	)
	return root
}

func newVersionCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(opts.stdout, "ouroboros", version)
			return err
		},
	}
}

func newConfigCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Create or validate the configuration file",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "init",
			Short: "Print an example configuration",
			Long: "Print an annotated example configuration to standard output.\n" +
				"Redirect it to a file and edit it, for example:\n" +
				`  ouroboros config init > C:\ProgramData\Ouroboros\ouroboros.toml`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				_, err := opts.stdout.Write(ouroboros.ExampleConfig)
				return err
			},
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Check the configuration file for errors",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return validateConfig(opts, platformDisk{})
			},
		},
	)
	return cmd
}

// diskUsager reports the size and free space of the partition holding path.
type diskUsager interface {
	DiskUsage(path string) (total, free uint64, err error)
}

func validateConfig(opts *options, disk diskUsager) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	total, _, err := disk.DiskUsage(cfg.Repository.Path)
	if err != nil {
		fmt.Fprintf(opts.stdout, "%s: valid\n", opts.configPath)
		fmt.Fprintf(opts.stderr, "warning: partition size checks skipped: %v\n", err)
		return &codedError{code: exitWarnings}
	}
	if err := cfg.ValidateDisk(total); err != nil {
		return fmt.Errorf("%s: %w", opts.configPath, err)
	}
	fmt.Fprintf(opts.stdout, "%s: valid\n", opts.configPath)
	return nil
}
