package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	errEnvConflict     = errors.New("cli: conflicting environment configuration")
	errInvalidValue    = errors.New("cli: invalid value")
	errMissingRequired = errors.New("cli: missing required configuration")
)

// dispatch carries one execution's flag state: declared sets per command,
// and which flag names were explicitly set on the command line.
type dispatch struct {
	root     *Command
	decls    map[*Command]*FlagSet
	explicit map[string]bool
}

// declared builds (once per execution) the FlagSet a command declares.
func (d *dispatch) declared(c *Command) *FlagSet {
	if fs, ok := d.decls[c]; ok {
		return fs
	}
	fs := newFlagSet()
	if c.Flags != nil {
		c.Flags(fs)
	}
	if c == d.root && c.Config.Flag != "" && fs.inner.Lookup(c.Config.Flag) == nil {
		fs.String(c.Config.Flag, "", "config file path")
	}
	d.decls[c] = fs
	return fs
}

// parseSet merges the visible flags along path into one parseable set.
// The returned *bool is non-nil when the auto --version flag was added.
func (d *dispatch) parseSet(path []*Command) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet(pathName(path), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	for _, c := range path {
		d.declared(c).inner.VisitAll(func(f *flag.Flag) {
			if fs.Lookup(f.Name) != nil {
				panic("cli: flag redeclared in " + pathName(path) + ": -" + f.Name)
			}
			fs.Var(f.Value, f.Name, f.Usage)
		})
	}
	var showVersion *bool
	if len(path) == 1 && d.root.Version != "" && fs.Lookup("version") == nil {
		showVersion = fs.Bool("version", false, "print the version")
	}
	return fs, showVersion
}

// secrets collects the secret-marked flag names visible along path.
func (d *dispatch) secrets(path []*Command) map[string]bool {
	m := map[string]bool{}
	for _, c := range path {
		for name := range d.declared(c).secret {
			m[name] = true
		}
	}
	return m
}

// visible calls fn for every flag visible along path, lexicographically
// per command (flag.VisitAll order), root first.
func (d *dispatch) visible(path []*Command, fn func(owner *FlagSet, f *flag.Flag)) {
	for _, c := range path {
		owner := d.declared(c)
		owner.inner.VisitAll(func(f *flag.Flag) { fn(owner, f) })
	}
}

// resolve applies the environment, *_FILE, and config file layers to
// every visible flag not explicitly set on the command line.
func (d *dispatch) resolve(path []*Command) error {
	prefix := envPrefix(d.root)
	fromEnv := map[string]bool{}

	var err error
	d.visible(path, func(owner *FlagSet, f *flag.Flag) {
		if err != nil || d.explicit[f.Name] {
			return
		}
		set, envErr := setFromEnv(f, envName(prefix, f.Name), owner.secret[f.Name])
		if envErr != nil {
			err = envErr
			return
		}
		if set {
			fromEnv[f.Name] = true
		}
	})
	if err != nil {
		return err
	}

	values, err := d.loadConfig(fromEnv)
	if err != nil {
		return err
	}
	fromConfig := map[string]bool{}
	d.visible(path, func(owner *FlagSet, f *flag.Flag) {
		if err != nil || d.explicit[f.Name] || fromEnv[f.Name] {
			return
		}
		v, ok := values[f.Name]
		if !ok {
			return
		}
		if setErr := f.Value.Set(v); setErr != nil {
			err = setValueError(f.Name, v, "config file", owner.secret[f.Name])
			return
		}
		fromConfig[f.Name] = true
	})
	if err != nil {
		return err
	}

	var missing []string
	d.visible(path, func(owner *FlagSet, f *flag.Flag) {
		if owner.required[f.Name] && !d.explicit[f.Name] && !fromEnv[f.Name] && !fromConfig[f.Name] {
			missing = append(missing, fmt.Sprintf("-%s (env %s)", f.Name, envName(prefix, f.Name)))
		}
	})
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", errMissingRequired, strings.Join(missing, ", "))
	}
	return nil
}

// setFromEnv resolves one flag against its environment variable and the
// *_FILE indirection. It reports whether a value was applied.
func setFromEnv(f *flag.Flag, env string, secret bool) (bool, error) {
	value, plain := os.LookupEnv(env)
	file, indirect := os.LookupEnv(env + "_FILE")
	switch {
	case plain && indirect:
		return false, fmt.Errorf("%w: both %s and %s_FILE are set", errEnvConflict, env, env)
	case indirect:
		b, err := os.ReadFile(file) // #nosec G304 -- the operator's own *_FILE indirection; reading it is the feature
		if err != nil {
			return false, fmt.Errorf("cli: reading %s_FILE: %w", env, err)
		}
		value = strings.TrimRight(string(b), "\r\n")
	case !plain:
		return false, nil
	}
	if err := f.Value.Set(value); err != nil {
		return false, setValueError(f.Name, value, env, secret)
	}
	return true, nil
}

// setValueError formats a value-rejection error, omitting the value for
// secret flags so credentials never reach stderr or logs.
func setValueError(name, value, source string, secret bool) error {
	if secret {
		return fmt.Errorf("%w for flag -%s from %s", errInvalidValue, name, source)
	}
	return fmt.Errorf("%w %q for flag -%s from %s", errInvalidValue, value, name, source)
}

// envName maps a flag name to its environment variable: uppercase, with
// every non-alphanumeric rune replaced by underscore, prefixed.
func envName(prefix, flagName string) string {
	mapped := envMap(flagName)
	if prefix == "" {
		return mapped
	}
	return prefix + "_" + mapped
}

func envPrefix(root *Command) string {
	switch root.EnvPrefix {
	case NoPrefix:
		return ""
	case "":
		return envMap(root.Name)
	default:
		return envMap(root.EnvPrefix)
	}
}

func envMap(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
