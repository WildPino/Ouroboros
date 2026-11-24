package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/WildPino/ouroboros/internal/backend/restic"
	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/secret"
)

// secretPath returns the password file that belongs to a configuration: it
// lives in the same directory (DESIGN.md §5).
func secretPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), secret.FileName)
}

// newResticBackend builds the restic backend for cfg. restic obtains the
// password by running this executable's "internal password" command with
// the same configuration path, so the password never leaves the process
// pipes.
func newResticBackend(cfg *config.Config, configPath string) (*restic.Restic, error) {
	exe, err := restic.FindExecutable(cfg.Repository.ResticPath)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate ouroboros executable: %w", err)
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	return restic.New(restic.Options{
		Executable:      exe,
		Repository:      cfg.Repository.Path,
		PasswordCommand: restic.PasswordCommand(self, "--config", absConfig),
		Compression:     cfg.Repository.Compression,
		PackSizeMB:      cfg.Performance.PackSizeMB,
		ReadConcurrency: cfg.Performance.ReadConcurrency,
		Priority:        cfg.Performance.ProcessPriority,
	})
}

// newInternalCmd groups commands that only Ouroboros itself calls.
func newInternalCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Short:  "Commands used by Ouroboros itself",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "password",
		Short: "Print the repository password for restic --password-command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pw, err := secret.NewStore(secretPath(opts.configPath)).Load()
			if err != nil {
				return err
			}
			defer secret.Clear(pw)
			_, err = opts.stdout.Write(pw)
			return err
		},
	})
	return cmd
}
