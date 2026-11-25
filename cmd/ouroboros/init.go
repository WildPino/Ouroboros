package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/platform/windows"
	"github.com/WildPino/ouroboros/internal/secret"
	"github.com/WildPino/ouroboros/internal/space"
)

const minPasswordLength = 8

// initDeps holds what init needs from the system, replaceable in tests.
type initDeps struct {
	isElevated   func() (bool, error)
	readPassword func(prompt string) ([]byte, error)
	newStore     func(path string) *secret.Store
	newBackend   func(cfg *config.Config, configPath string) (repoIniter, error)
	disk         diskUsager
	// stdin, when set, supplies the password on one line instead of the
	// console (--password-stdin).
	stdin io.Reader
}

type repoIniter interface {
	Init(ctx context.Context) error
}

func newInitCmd(opts *options) *cobra.Command {
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the repository and store its password",
		Long: "Create the restic repository configured in repository.path, store its password\n" +
			"encrypted for this machine, and reserve the emergency ballast file.\n" +
			"Must be run as Administrator. The password is asked twice on the console, or\n" +
			"read once from the first line of standard input with --password-stdin.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := initDeps{
				isElevated:   windows.IsElevated,
				readPassword: consolePassword(opts.stderr),
				newStore:     secret.NewStore,
				newBackend: func(cfg *config.Config, p string) (repoIniter, error) {
					return newResticBackend(cfg, p)
				},
				disk: platformDisk{},
			}
			if passwordStdin {
				d.stdin = os.Stdin
			}
			return runInit(cmd.Context(), opts, d)
		},
	}
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from the first line of standard input")
	return supportsJSON(cmd)
}

// initOut is the result of "init --json".
type initOut struct {
	Repository   string `json:"repository"`
	PasswordFile string `json:"password_file"`
}

func runInit(ctx context.Context, opts *options, d initDeps) error {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return err
	}
	elevated, err := d.isElevated()
	if err != nil {
		return fmt.Errorf("check privileges: %w", err)
	}
	if !elevated {
		return errors.New("init must be run as Administrator, so that the password file can be protected")
	}

	repo := cfg.Repository.Path
	if err := checkEmptyOrMissing(repo); err != nil {
		return err
	}
	store := d.newStore(secretPath(opts.configPath))
	switch exists, err := store.Exists(); {
	case err != nil:
		return err
	case exists:
		return fmt.Errorf("a repository password is already stored in %s; remove it first only if no repository uses it", store.Path())
	}

	var pw []byte
	if d.stdin != nil {
		pw, err = readPasswordLine(d.stdin)
		if err == nil {
			if err = checkNewPassword(pw); err != nil {
				secret.Clear(pw)
			}
		}
	} else {
		pw, err = askNewPassword(d.readPassword)
	}
	if err != nil {
		return err
	}
	defer secret.Clear(pw)
	// With --json the text below would break the output lines.
	stdout := opts.stdout
	if opts.out != nil {
		stdout = io.Discard
	}

	b, err := d.newBackend(cfg, opts.configPath)
	if err != nil {
		return err
	}
	if err := store.Save(pw); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Password stored in %s\n", store.Path())
	if err := b.Init(ctx); err != nil {
		// Without a repository the stored password is useless and would
		// block the next attempt.
		if rmErr := store.Remove(); rmErr != nil {
			fmt.Fprintf(opts.stderr, "warning: could not remove %s: %v\n", store.Path(), rmErr)
		}
		return fmt.Errorf("create repository: %w", err)
	}
	fmt.Fprintf(stdout, "Repository created in %s\n", repo)

	warn := reserveBallast(opts, cfg, d.disk)

	if opts.out != nil {
		opts.out.result(initOut{Repository: repo, PasswordFile: store.Path()})
	}
	fmt.Fprint(stdout, "\n"+
		"IMPORTANT: write down the repository password and keep it somewhere other\n"+
		"than this computer. Without it the backups cannot be decrypted, by Ouroboros\n"+
		"or by anyone else.\n")
	if warn {
		return &codedError{code: exitWarnings}
	}
	return nil
}

// reserveBallast creates the ballast file if it is enabled and fits without
// eating into min_free. It reports whether a warning was printed.
func reserveBallast(opts *options, cfg *config.Config, disk diskUsager) bool {
	size := cfg.Space.ReserveFileSize.Resolve(0)
	if size == 0 {
		return false
	}
	path := space.BallastPath(cfg.Repository.Path)
	total, free, err := disk.DiskUsage(cfg.Repository.Path)
	if err != nil {
		fmt.Fprintf(opts.stderr, "warning: ballast not created, cannot read free space: %v\n", err)
		return true
	}
	if minFree := cfg.Space.MinFree.Resolve(total); free < size || free-size < minFree {
		fmt.Fprintf(opts.stderr, "warning: ballast not created: %s free, need %s for the ballast plus %s min_free\n",
			formatBytes(free), formatBytes(size), formatBytes(minFree))
		return true
	}
	if err := space.CreateBallast(path, size); err != nil {
		fmt.Fprintf(opts.stderr, "warning: ballast not created: %v\n", err)
		return true
	}
	if opts.out == nil {
		fmt.Fprintf(opts.stdout, "Ballast of %s reserved in %s\n", formatBytes(size), path)
	}
	return false
}

// checkEmptyOrMissing refuses to initialize over existing data.
func checkEmptyOrMissing(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s already exists and is not empty", dir)
	}
	return nil
}

func askNewPassword(read func(string) ([]byte, error)) ([]byte, error) {
	pw, err := read("Repository password: ")
	if err != nil {
		return nil, err
	}
	again, err := read("Repeat password: ")
	if err != nil {
		secret.Clear(pw)
		return nil, err
	}
	defer secret.Clear(again)
	if subtle.ConstantTimeCompare(pw, again) != 1 {
		secret.Clear(pw)
		return nil, errors.New("passwords do not match")
	}
	if err := checkNewPassword(pw); err != nil {
		secret.Clear(pw)
		return nil, err
	}
	return pw, nil
}

// checkNewPassword applies the rules for a new repository password. Its
// errors never contain the password.
func checkNewPassword(pw []byte) error {
	if len(pw) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	// restic trims what the password command prints, so surrounding blanks
	// would be lost when restic reads the password back.
	if !bytes.Equal(bytes.TrimSpace(pw), pw) {
		return errors.New("password must not start or end with spaces")
	}
	return nil
}

// maxPasswordBytes bounds what --password-stdin reads.
const maxPasswordBytes = 1024

// readPasswordLine reads the password from the first line of r, without a
// trailing CR LF or LF. It reads one byte at a time so that no copy of the
// password stays in a buffer it cannot clear, and so that it does not wait
// for the writer to close the stream after the line.
func readPasswordLine(r io.Reader) ([]byte, error) {
	pw := make([]byte, 0, maxPasswordBytes)
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			if len(pw) == maxPasswordBytes {
				secret.Clear(pw[:cap(pw)])
				b[0] = 0
				return nil, fmt.Errorf("password on standard input is longer than %d bytes", maxPasswordBytes)
			}
			pw = append(pw, b[0])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			secret.Clear(pw[:cap(pw)])
			return nil, fmt.Errorf("read the password from standard input: %w", err)
		}
	}
	b[0] = 0
	if n := len(pw); n > 0 && pw[n-1] == '\r' {
		pw[n-1] = 0
		pw = pw[:n-1]
	}
	if len(pw) == 0 {
		return nil, errors.New("no password on standard input")
	}
	return pw, nil
}

// consolePassword reads a password from the console without echoing it.
func consolePassword(prompt io.Writer) func(string) ([]byte, error) {
	return func(text string) ([]byte, error) {
		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			return nil, errors.New("init needs an interactive console to read the password")
		}
		fmt.Fprint(prompt, text)
		pw, err := term.ReadPassword(fd)
		fmt.Fprintln(prompt)
		return pw, err
	}
}
