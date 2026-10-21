package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/compose"
	"github.com/azverinus/flagnetic/internal/infra/config"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

const adminUsage = `Usage: flagnetic admin <command>

Commands:
  bootstrap --email <email> [--name <name>] [--token-file <path>] [--if-absent]
      Create the first instance admin with a password and an API token.
      The password is FLAGNETIC_ADMIN_PASSWORD, or one generated and printed
      once on standard error. The token, carrying every scope and no expiry,
      is printed on standard output, or written to the --token-file instead:
      a new file of mode 0600, created before anything else, so a path that
      exists or cannot be created stops the command with nothing created.
      Refused once an instance admin exists; with --if-absent that is a
      success that does nothing.
`

// commandLine carries what a command reads besides its configuration.
type commandLine struct {
	args           []string
	lookupEnv      func(string) (string, bool)
	stdout, stderr io.Writer
}

// adminHelp answers an admin command line naming no known command, before
// any configuration is read: help on request, the usage and exit 2 otherwise.
func adminHelp(args []string, stdout, stderr io.Writer) (code int, done bool) {
	switch {
	case len(args) > 0 && args[0] == "bootstrap":
		return 0, false
	case len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		fmt.Fprint(stdout, adminUsage)
		return exitOK, true
	}
	fmt.Fprint(stderr, adminUsage)
	return exitUsage, true
}

func admin(ctx context.Context, cl commandLine, cfg config.Config, log *slog.Logger, o options) int {
	cl.args = cl.args[1:]
	return adminBootstrap(ctx, cl, cfg, log, o)
}

func adminBootstrap(ctx context.Context, cl commandLine, cfg config.Config, log *slog.Logger, o options) int {
	fs := flag.NewFlagSet("admin bootstrap", flag.ContinueOnError)
	fs.SetOutput(cl.stderr)
	email := fs.String("email", "", "email of the instance admin (required)")
	name := fs.String("name", "", "display name of the instance admin; the email's local part when absent")
	tokenFile := fs.String("token-file", "", "write the token to this new file, mode 0600, instead of standard output")
	ifAbsent := fs.Bool("if-absent", false, "succeed without doing anything when an instance admin exists")
	if err := fs.Parse(cl.args); err != nil {
		return exitUsage
	}
	if *email == "" || fs.NArg() > 0 {
		fmt.Fprint(cl.stderr, "admin bootstrap: --email is required, and nothing else\n\n"+adminUsage)
		return exitUsage
	}

	pool, err := openDatabase(ctx, cfg, log, o)
	if err != nil {
		return fail(log, "admin bootstrap failed", err)
	}
	defer pool.Close()
	if err = checkSchema(ctx, pool, log, o); err != nil {
		return fail(log, "admin bootstrap failed", err)
	}

	deps := compose.NewDeps(pool, clock.System{})
	if *ifAbsent {
		// Asked first, so a provisioning step rerun on every deploy finds
		// nothing to do before it touches the token file it wrote last time.
		exists, existsErr := compose.InstanceAdminExists(deps).Handle(ctx)
		if err = existsErr; err != nil {
			return fail(log, "admin bootstrap failed", err)
		}
		if exists {
			fmt.Fprintln(cl.stderr, "admin bootstrap: an instance admin exists; nothing done")
			return exitOK
		}
	}
	var out *secretFile
	if *tokenFile != "" {
		if out, err = createSecretFile(*tokenFile); err != nil {
			fmt.Fprintf(cl.stderr, "admin bootstrap: cannot create the token file: %v; nothing was created\n", err)
			return exitFailure
		}
		defer out.discard()
	}

	password, _ := cl.lookupEnv("FLAGNETIC_ADMIN_PASSWORD")
	res, err := compose.Bootstrap(deps).Handle(ctx, appaccess.BootstrapCommand{
		Email: *email, DisplayName: *name, Password: secret.New(password),
		Request: governance.Request{Actor: governance.CLIActor("admin bootstrap")},
	})
	if e, ok := apperrors.From(err); ok {
		switch {
		case e.Code == appaccess.ErrInstanceAdminExists.Code && *ifAbsent:
			fmt.Fprintln(cl.stderr, "admin bootstrap: an instance admin exists; nothing done")
			return exitOK
		case e.Kind == apperrors.KindInvalid:
			fmt.Fprintf(cl.stderr, "admin bootstrap: %s\n", describe(e))
			return exitUsage
		default:
			fmt.Fprintf(cl.stderr, "admin bootstrap: %s\n", e.Detail)
			return exitFailure
		}
	}
	if err != nil {
		return fail(log, "admin bootstrap failed", err)
	}

	if !res.GeneratedPassword.IsZero() {
		fmt.Fprintf(cl.stderr, "The instance admin's password is shown once; store it now:\nPassword: %s\n", res.GeneratedPassword.Reveal())
	}
	if out != nil {
		if err := out.write(res.Token.Reveal() + "\n"); err != nil {
			// The operator chose a file to keep the token out of logs, so it
			// is not printed instead: the instance admin signs in with their
			// password and creates another token.
			fmt.Fprintf(cl.stderr, "admin bootstrap: the instance admin was created, but writing the token to %s failed: %v\n", *tokenFile, err)
			return exitFailure
		}
		fmt.Fprintf(cl.stderr, "Instance admin created. Their API token is in %s.\n", *tokenFile)
		return exitOK
	}
	fmt.Fprintln(cl.stderr, "Instance admin created. This API token is shown once; store it now:")
	fmt.Fprintln(cl.stdout, res.Token.Reveal())
	return exitOK
}

// describe returns an invalid input's detail with its violations.
func describe(e *apperrors.Error) string {
	s := e.Detail
	for _, v := range e.Violations {
		s += "; " + v.Pointer + ": " + v.Detail
	}
	return s
}

// secretFile is a file a secret is written to, created before anything the
// secret belongs to, so a path that cannot take it stops the command before
// it creates anything.
type secretFile struct {
	f    *os.File
	kept bool
}

// createSecretFile creates path, readable by its owner alone. A path that
// exists, a symbolic link included, is refused: overwriting a file the
// operator did not mean, or following a link someone else placed, could hand
// the secret to another reader.
func createSecretFile(path string) (*secretFile, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, err
	}
	// The mode is set again since the process's umask applies to creation.
	if err := f.Chmod(0o600); err != nil {
		return nil, errors.Join(err, f.Close(), os.Remove(path))
	}
	return &secretFile{f: f}, nil
}

// write writes content to the file and closes it; a file that did not take
// all of it is removed, so no partial secret is left behind.
func (s *secretFile) write(content string) error {
	_, err := s.f.WriteString(content)
	if err = errors.Join(err, s.f.Sync(), s.f.Close()); err != nil {
		return errors.Join(err, os.Remove(s.f.Name()))
	}
	s.kept = true
	return nil
}

// discard removes the file unless a secret was written to it.
func (s *secretFile) discard() {
	if !s.kept {
		_ = s.f.Close()
		_ = os.Remove(s.f.Name())
	}
}
