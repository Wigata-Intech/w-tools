package cli

import (
	"flag"
	"io"
	"time"
)

// FlagSet declares a command's flags. It mirrors the flag.FlagSet
// constructors exactly — String, Bool, Int, Int64, Float64, Duration,
// their *Var forms, and Var — and adds only Secret. Values set from the
// environment or a config file flow through the same flag.Value.Set path
// as command-line input.
type FlagSet struct {
	inner    *flag.FlagSet
	secret   map[string]bool
	required map[string]bool
	zero     map[string]bool
}

func newFlagSet() *FlagSet {
	inner := flag.NewFlagSet("", flag.ContinueOnError)
	inner.SetOutput(io.Discard)
	inner.Usage = func() {}
	return &FlagSet{
		inner:    inner,
		secret:   map[string]bool{},
		required: map[string]bool{},
		zero:     map[string]bool{},
	}
}

// Bool defines a bool flag with specified name, default value, and usage
// string. The return value is the address of a bool variable that stores
// the value of the flag.
func (fs *FlagSet) Bool(name string, value bool, usage string) *bool {
	fs.zero[name] = !value
	return fs.inner.Bool(name, value, usage)
}

// BoolVar defines a bool flag with specified name, default value, and
// usage string. The argument p points to a bool variable in which to
// store the value of the flag.
func (fs *FlagSet) BoolVar(p *bool, name string, value bool, usage string) {
	fs.zero[name] = !value
	fs.inner.BoolVar(p, name, value, usage)
}

// Duration defines a time.Duration flag with specified name, default
// value, and usage string. The return value is the address of a
// time.Duration variable that stores the value of the flag.
func (fs *FlagSet) Duration(name string, value time.Duration, usage string) *time.Duration {
	fs.zero[name] = value == 0
	return fs.inner.Duration(name, value, usage)
}

// DurationVar defines a time.Duration flag with specified name, default
// value, and usage string. The argument p points to a time.Duration
// variable in which to store the value of the flag.
func (fs *FlagSet) DurationVar(p *time.Duration, name string, value time.Duration, usage string) {
	fs.zero[name] = value == 0
	fs.inner.DurationVar(p, name, value, usage)
}

// Float64 defines a float64 flag with specified name, default value, and
// usage string. The return value is the address of a float64 variable
// that stores the value of the flag.
func (fs *FlagSet) Float64(name string, value float64, usage string) *float64 {
	fs.zero[name] = value == 0
	return fs.inner.Float64(name, value, usage)
}

// Float64Var defines a float64 flag with specified name, default value,
// and usage string. The argument p points to a float64 variable in which
// to store the value of the flag.
func (fs *FlagSet) Float64Var(p *float64, name string, value float64, usage string) {
	fs.zero[name] = value == 0
	fs.inner.Float64Var(p, name, value, usage)
}

// Int defines an int flag with specified name, default value, and usage
// string. The return value is the address of an int variable that stores
// the value of the flag.
func (fs *FlagSet) Int(name string, value int, usage string) *int {
	fs.zero[name] = value == 0
	return fs.inner.Int(name, value, usage)
}

// IntVar defines an int flag with specified name, default value, and
// usage string. The argument p points to an int variable in which to
// store the value of the flag.
func (fs *FlagSet) IntVar(p *int, name string, value int, usage string) {
	fs.zero[name] = value == 0
	fs.inner.IntVar(p, name, value, usage)
}

// Int64 defines an int64 flag with specified name, default value, and
// usage string. The return value is the address of an int64 variable
// that stores the value of the flag.
func (fs *FlagSet) Int64(name string, value int64, usage string) *int64 {
	fs.zero[name] = value == 0
	return fs.inner.Int64(name, value, usage)
}

// Int64Var defines an int64 flag with specified name, default value, and
// usage string. The argument p points to an int64 variable in which to
// store the value of the flag.
func (fs *FlagSet) Int64Var(p *int64, name string, value int64, usage string) {
	fs.zero[name] = value == 0
	fs.inner.Int64Var(p, name, value, usage)
}

// String defines a string flag with specified name, default value, and
// usage string. The return value is the address of a string variable
// that stores the value of the flag.
func (fs *FlagSet) String(name, value, usage string) *string {
	fs.zero[name] = value == ""
	return fs.inner.String(name, value, usage)
}

// StringVar defines a string flag with specified name, default value,
// and usage string. The argument p points to a string variable in which
// to store the value of the flag.
func (fs *FlagSet) StringVar(p *string, name, value, usage string) {
	fs.zero[name] = value == ""
	fs.inner.StringVar(p, name, value, usage)
}

// Var defines a flag with the specified name and usage string, backed by
// the given flag.Value.
func (fs *FlagSet) Var(value flag.Value, name, usage string) {
	fs.zero[name] = value.String() == ""
	fs.inner.Var(value, name, usage)
}

// Secret marks a declared flag as secret: generated help masks its
// default value and error messages never echo its value. Marking an
// undeclared flag panics — a programmer error, caught at boot.
func (fs *FlagSet) Secret(name string) {
	if fs.inner.Lookup(name) == nil {
		panic("cli: Secret on undeclared flag: -" + name)
	}
	fs.secret[name] = true
}
