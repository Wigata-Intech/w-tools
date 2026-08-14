package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Command is one node of a command tree. The zero value is unusable; set
// at least Name. Fields marked root-only are ignored on subcommands.
type Command struct {
	// Name is the binary name on the root command and the verb on a
	// subcommand ("serve", "migrate").
	Name string

	// Short is the one-line description shown in the parent's command list.
	Short string

	// Long is the description shown in this command's own help; Short is
	// used when Long is empty.
	Long string

	// Flags declares this command's flags on the given set. Declared
	// flags are visible to this command and all of its descendants;
	// redeclaring an inherited name panics at dispatch.
	Flags func(fs *FlagSet)

	// Run executes the command; args are the operands left after flag
	// parsing. A command with subcommands may leave Run nil — invoking it
	// bare then prints help.
	Run func(ctx context.Context, args []string) error

	// Commands are the subcommands, matched by Name.
	Commands []*Command

	// Version enables the version subcommand and the --version flag,
	// printing "<name> <version>". Root-only.
	Version string

	// EnvPrefix overrides the environment prefix derived from Name; the
	// NoPrefix sentinel disables prefixing. Root-only.
	EnvPrefix string

	// Config enables config file support; the zero value disables it.
	// Root-only.
	Config ConfigFile

	stdout io.Writer
	stderr io.Writer
}

// Execute parses os.Args[1:], dispatches through the tree, resolves every
// visible flag with the documented precedence, and runs the resolved
// command. It returns the process exit code: 0 success, 1 the command's
// Run returned an error, 2 usage, environment, or configuration error.
func (c *Command) Execute(ctx context.Context) int {
	return c.execute(ctx, os.Args[1:])
}

func (c *Command) execute(ctx context.Context, args []string) int {
	out, errw := c.out(), c.errOut()

	d := &dispatch{root: c, decls: map[*Command]*FlagSet{}, explicit: map[string]bool{}}
	path := []*Command{c}
	rest := args

	for {
		cur := path[len(path)-1]
		fs, showVersion := d.parseSet(path)

		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				writeHelp(out, d, path)
				return 0
			}
			_, _ = fmt.Fprintln(errw, sanitizeParseError(err, d.secrets(path)))
			_, _ = fmt.Fprintf(errw, "Run '%s --help' for usage.\n", pathName(path))
			return 2
		}
		fs.Visit(func(f *flag.Flag) { d.explicit[f.Name] = true })

		if showVersion != nil && *showVersion {
			_, _ = fmt.Fprintf(out, "%s %s\n", c.Name, c.Version)
			return 0
		}

		operands := fs.Args()
		terminated := false
		if consumed := len(rest) - len(operands); consumed > 0 && rest[consumed-1] == "--" {
			terminated = true
		}
		rest = operands
		if len(rest) == 0 || terminated {
			break
		}
		if next := findCommand(cur.Commands, rest[0]); next != nil {
			path = append(path, next)
			rest = rest[1:]
			continue
		}
		if len(path) == 1 && len(c.Commands) > 0 {
			switch rest[0] {
			case "help":
				return c.helpCommand(out, errw, d, rest[1:])
			case "version":
				if c.Version != "" {
					_, _ = fmt.Fprintf(out, "%s %s\n", c.Name, c.Version)
					return 0
				}
			}
		}
		break
	}

	resolved := path[len(path)-1]
	if resolved.Run == nil {
		if len(rest) > 0 {
			_, _ = fmt.Fprintf(errw, "%s: unknown command: %s\n", pathName(path), rest[0])
			_, _ = fmt.Fprintf(errw, "Run '%s --help' for usage.\n", pathName(path))
			return 2
		}
		writeHelp(out, d, path)
		return 0
	}

	if err := d.resolve(path); err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 2
	}

	if err := resolved.Run(ctx, rest); err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	return 0
}

// helpCommand serves `<root> help [command ...]`.
func (c *Command) helpCommand(out, errw io.Writer, d *dispatch, names []string) int {
	path := []*Command{c}
	for _, name := range names {
		next := findCommand(path[len(path)-1].Commands, name)
		if next == nil {
			_, _ = fmt.Fprintf(errw, "%s: unknown command: %s\n", c.Name, strings.Join(names, " "))
			_, _ = fmt.Fprintf(errw, "Run '%s --help' for usage.\n", c.Name)
			return 2
		}
		path = append(path, next)
	}
	writeHelp(out, d, path)
	return 0
}

func findCommand(cmds []*Command, name string) *Command {
	for _, c := range cmds {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func pathName(path []*Command) string {
	names := make([]string, len(path))
	for i, c := range path {
		names[i] = c.Name
	}
	return strings.Join(names, " ")
}

// sanitizeParseError strips the echoed value from stdlib flag's
// invalid-value errors when the flag is secret-marked. Two patterns:
// flag formats most errors "for flag -name:" but bool errors "for -name:".
func sanitizeParseError(err error, secrets map[string]bool) string {
	msg := err.Error()
	for name := range secrets {
		if strings.Contains(msg, "for flag -"+name+":") || strings.Contains(msg, "for -"+name+":") {
			return "invalid value for flag -" + name
		}
	}
	return msg
}

func (c *Command) out() io.Writer {
	if c.stdout != nil {
		return c.stdout
	}
	return os.Stdout
}

func (c *Command) errOut() io.Writer {
	if c.stderr != nil {
		return c.stderr
	}
	return os.Stderr
}
