package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
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

  create --email <email> [--name <name>]
      Add an active instance admin with a password: FLAGNETIC_ADMIN_PASSWORD,
      or one generated and printed once on standard output.

  promote --email <email>
      Make the member with this email an instance admin.

A password given in FLAGNETIC_ADMIN_PASSWORD must pass the password policy:
12 to 128 characters, not containing the part of the email before the @,
and not a common password. A refused one exits 2 naming the rule.
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
	case len(args) > 0 && (args[0] == "bootstrap" || args[0] == "create" || args[0] == "promote"):
		return 0, false
	case len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		fmt.Fprint(stdout, adminUsage)
		return exitOK, true
	}
	fmt.Fprint(stderr, adminUsage)
	return exitUsage, true
}

func admin(ctx context.Context, cl commandLine, cfg config.Config, log *slog.Logger, o options) int {
	command := cl.args[0]
	cl.args = cl.args[1:]
	switch command {
	case "create":
		return adminCreate(ctx, cl, cfg, log, o)
	case "promote":
		return adminPromote(ctx, cl, cfg, log, o)
	}
	return adminBootstrap(ctx, cl, cfg, log, o)
}

// adminDatabase opens the migrated database an admin command works on, or
// returns the exit code of the failure.
func adminDatabase(ctx context.Context, command string, cfg config.Config, log *slog.Logger, o options) (*pgxpool.Pool, int) {
	pool, err := openDatabase(ctx, cfg, log, o)
	if err != nil {
		return nil, fail(log, "admin "+command+" failed", err)
	}
	if err = checkSchema(ctx, pool, log, o); err != nil {
		pool.Close()
		return nil, fail(log, "admin "+command+" failed", err)
	}
	return pool, exitOK
}

// adminRefusal reports a refusal of an admin command on stderr and returns
// its exit code: 2 for input to fix, such as a weak password, 1 otherwise.
func adminRefusal(cl commandLine, command string, e *apperrors.Error) int {
	if e.Kind == apperrors.KindInvalid {
		fmt.Fprintf(cl.stderr, "admin %s: %s\n", command, describe(e))
		return exitUsage
	}
	fmt.Fprintf(cl.stderr, "admin %s: %s\n", command, e.Detail)
	return exitFailure
}

func adminCreate(ctx context.Context, cl commandLine, cfg config.Config, log *slog.Logger, o options) int {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(cl.stderr)
	email := fs.String("email", "", "email of the instance admin (required)")
	name := fs.String("name", "", "display name of the instance admin; the email's local part when absent")
	if err := fs.Parse(cl.args); err != nil {
		return exitUsage
	}
	if *email == "" || fs.NArg() > 0 {
		fmt.Fprint(cl.stderr, "admin create: --email is required, and nothing else\n\n"+adminUsage)
		return exitUsage
	}
	pool, code := adminDatabase(ctx, "create", cfg, log, o)
	if pool == nil {
		return code
	}
	defer pool.Close()

	password, _ := cl.lookupEnv("FLAGNETIC_ADMIN_PASSWORD")
	res, err := compose.CreateInstanceAdmin(compose.NewDeps(pool, clock.System{})).Handle(ctx, appaccess.CreateInstanceAdminCommand{
		Email: *email, DisplayName: *name, Password: secret.New(password),
		Request: governance.Request{Actor: governance.CLIActor("admin create")},
	})
	if e, ok := apperrors.From(err); ok {
		return adminRefusal(cl, "create", e)
	}
	if err != nil {
		return fail(log, "admin create failed", err)
	}
	if res.GeneratedPassword.IsZero() {
		fmt.Fprintln(cl.stderr, "Instance admin created with the password in FLAGNETIC_ADMIN_PASSWORD.")
		return exitOK
	}
	fmt.Fprintln(cl.stderr, "Instance admin created. This password is shown once; store it now:")
	fmt.Fprintln(cl.stdout, res.GeneratedPassword.Reveal())
	return exitOK
}

func adminPromote(ctx context.Context, cl commandLine, cfg config.Config, log *slog.Logger, o options) int {
	fs := flag.NewFlagSet("admin promote", flag.ContinueOnError)
	fs.SetOutput(cl.stderr)
	email := fs.String("email", "", "email of the member to promote (required)")
	if err := fs.Parse(cl.args); err != nil {
		return exitUsage
	}
	if *email == "" || fs.NArg() > 0 {
		fmt.Fprint(cl.stderr, "admin promote: --email is required, and nothing else\n\n"+adminUsage)
		return exitUsage
	}
	pool, code := adminDatabase(ctx, "promote", cfg, log, o)
	if pool == nil {
		return code
	}
	defer pool.Close()

	promoted, err := compose.PromoteMember(compose.NewDeps(pool, clock.System{})).Handle(ctx, *email,
		governance.Request{Actor: governance.CLIActor("admin promote")})
	if e, ok := apperrors.From(err); ok {
		return adminRefusal(cl, "promote", e)
	}
	if err != nil {
		return fail(log, "admin promote failed", err)
	}
	if !promoted {
		fmt.Fprintf(cl.stderr, "%s already is an instance admin; nothing done.\n", *email)
		return exitOK
	}
	fmt.Fprintf(cl.stderr, "%s is now an instance admin.\n", *email)
	return exitOK
}

// nothingToBootstrap is what --if-absent says on an instance that has an
// instance admin.
const nothingToBootstrap = "admin bootstrap: an instance admin exists; nothing done"

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
			fmt.Fprintln(cl.stderr, nothingToBootstrap)
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
			fmt.Fprintln(cl.stderr, nothingToBootstrap)
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

// describe returns an invalid input's detail with its violations; a
// password the policy refuses is named by its code and the rule it broke.
func describe(e *apperrors.Error) string {
	if rule := access.PasswordRule(e); rule != "" {
		return e.Code + " (" + rule + "): " + e.Violations[0].Detail
	}
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
