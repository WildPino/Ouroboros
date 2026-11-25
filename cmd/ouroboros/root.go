package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros"
	"github.com/WildPino/ouroboros/internal/config"
)

// options holds flags shared by all subcommands.
type options struct {
	configPath string
	stdout     io.Writer
	stderr     io.Writer

	// json selects the machine-readable output of docs/JSON.md; out is set
	// before a command that supports it runs.
	json bool
	out  *jsonLines
}

func defaultConfigPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "Ouroboros", "ouroboros.toml")
}

func newRootCmd(stdout, stderr io.Writer) (*cobra.Command, *options) {
	opts := &options{stdout: stdout, stderr: stderr}
	root := &cobra.Command{
		Use:           "ouroboros",
		Short:         "Space-aware, set-and-forget versioned backups for Windows",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !opts.json {
				return nil
			}
			if cmd.Annotations[jsonAnnotation] == "" {
				return fmt.Errorf("%s does not support --json", cmd.CommandPath())
			}
			opts.out = &jsonLines{w: opts.stdout, command: commandName(cmd)}
			return nil
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&opts.configPath, "config", defaultConfigPath(), "path to the configuration file")
	root.PersistentFlags().BoolVar(&opts.json, "json", false, "write machine-readable JSON Lines (see docs/JSON.md)")

	root.AddCommand(
		newVersionCmd(opts),
		newConfigCmd(opts),
		newDoctorCmd(opts),
		newInitCmd(opts),
		newRunCmd(opts),
		newStatusCmd(opts),
		newSnapshotsCmd(opts),
		newRestoreCmd(opts),
		newPinCmd(opts, true),
		newPinCmd(opts, false),
		newCheckCmd(opts),
		newInstallCmd(opts),
		newUninstallCmd(opts),
		newReclaimCmd(opts),
		newInternalCmd(opts),
	)
	return root, opts
}

// commandName is the command path without the program name, such as
// "status" or "config validate".
func commandName(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

func newVersionCmd(opts *options) *cobra.Command {
	return supportsJSON(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.out != nil {
				opts.out.result(map[string]string{"version": version})
				return nil
			}
			_, err := fmt.Fprintln(opts.stdout, "ouroboros", version)
			return err
		},
	})
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
