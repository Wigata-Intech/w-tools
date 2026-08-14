package cli

import (
	"flag"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Bind declares one flag per exported field of the struct v points to —
// the struct is the schema, and every configuration layer resolves into
// it through the declared flags.
//
// The `cli` tag names the flag and carries options; without it the name
// derives from the field name (HTTPAddr → http-addr, DBPassword →
// db-password). `cli:"-"` skips the field.
//
//	type Config struct {
//	    HTTPAddr   string        `cli:"http-addr" default:":8080" usage:"listen address"`
//	    DBPassword string        `cli:"db-password,secret,required" usage:"database password"`
//	    Timeout    time.Duration `usage:"request timeout"`
//	}
//
// Options: `secret` marks the flag as Secret; `required` makes startup
// fail with exit 2 unless some layer supplies a value. The `default` tag
// sets the default; without it the field's current value is the default.
// A field that is `required` cannot also carry a `default` — the pair is
// contradictory and panics at declaration.
//
// Supported field types: string, bool, int, int64, float64,
// time.Duration, and any field whose pointer implements flag.Value.
// Anything else panics at declaration — a programmer error, caught at
// boot. Unexported fields are skipped. A required field must start at
// its zero value.
//
// Bind assumes one Execute per process: without a default tag the
// field's current value is the default, so re-executing the same tree
// treats the previous run's result as the new default.
func (fs *FlagSet) Bind(v any) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		panic("cli: Bind requires a non-nil pointer to a struct")
	}
	rv = rv.Elem()
	rt := rv.Type()

	for i := range rt.NumField() {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := parseBindTag(field)
		if tag.name == "-" {
			continue
		}

		def, hasDef := field.Tag.Lookup("default")
		if hasDef && tag.required {
			panic("cli: Bind field " + field.Name + ": required and default are contradictory")
		}

		fs.declareField(rv.Field(i), field.Name, tag.name, def, hasDef, field.Tag.Get("usage"))
		if tag.secret {
			fs.Secret(tag.name)
		}
		if tag.required {
			fs.Required(tag.name)
		}
	}
}

// Required marks a declared flag as mandatory: unless some layer —
// command line, environment, *_FILE, or config file — supplies a value,
// Execute fails with exit 2 before Run. Marking an undeclared flag
// panics.
func (fs *FlagSet) Required(name string) {
	if fs.inner.Lookup(name) == nil {
		panic("cli: Required on undeclared flag: -" + name)
	}
	if !fs.zero[name] {
		panic("cli: Required on a flag with a non-zero default: -" + name)
	}
	fs.required[name] = true
}

// declareField routes one struct field to the matching typed declaration.
func (fs *FlagSet) declareField(fv reflect.Value, fieldName, name, def string, hasDef bool, usage string) {
	if fs.inner.Lookup(name) != nil {
		panic("cli: Bind field " + fieldName + ": flag -" + name + " already declared")
	}
	bad := func(err error) {
		panic(fmt.Sprintf("cli: Bind field %s: bad default %q: %v", fieldName, def, err))
	}
	switch p := fv.Addr().Interface().(type) {
	case *time.Duration:
		if hasDef {
			d, err := time.ParseDuration(def)
			if err != nil {
				bad(err)
			}
			*p = d
		}
		fs.DurationVar(p, name, *p, usage)
	case *string:
		if hasDef {
			*p = def
		}
		fs.StringVar(p, name, *p, usage)
	case *bool:
		if hasDef {
			b, err := strconv.ParseBool(def)
			if err != nil {
				bad(err)
			}
			*p = b
		}
		fs.BoolVar(p, name, *p, usage)
	case *int:
		if hasDef {
			n, err := strconv.Atoi(def)
			if err != nil {
				bad(err)
			}
			*p = n
		}
		fs.IntVar(p, name, *p, usage)
	case *int64:
		if hasDef {
			n, err := strconv.ParseInt(def, 10, 64)
			if err != nil {
				bad(err)
			}
			*p = n
		}
		fs.Int64Var(p, name, *p, usage)
	case *float64:
		if hasDef {
			f, err := strconv.ParseFloat(def, 64)
			if err != nil {
				bad(err)
			}
			*p = f
		}
		fs.Float64Var(p, name, *p, usage)
	default:
		value, ok := p.(flag.Value)
		if !ok {
			panic("cli: Bind field " + fieldName + ": unsupported type " + fv.Type().String())
		}
		if hasDef {
			if err := value.Set(def); err != nil {
				bad(err)
			}
		}
		fs.Var(value, name, usage)
	}
}

// bindTag is the parsed `cli` struct tag.
type bindTag struct {
	name     string
	secret   bool
	required bool
}

// parseBindTag reads the `cli` tag: name, then comma-separated options.
func parseBindTag(field reflect.StructField) bindTag {
	name, opts, _ := strings.Cut(field.Tag.Get("cli"), ",")
	if name == "" {
		name = kebab(field.Name)
	}
	tag := bindTag{name: name}
	for opts != "" {
		var opt string
		opt, opts, _ = strings.Cut(opts, ",")
		switch opt {
		case "secret":
			tag.secret = true
		case "required":
			tag.required = true
		default:
			panic("cli: Bind field " + field.Name + ": unknown option " + strconv.Quote(opt))
		}
	}
	return tag
}

// kebab derives a flag name from a Go field name, keeping initialism
// runs whole: HTTPAddr → http-addr, DBPassword → db-password, ID → id.
func kebab(field string) string {
	runes := []rune(field)
	var b strings.Builder
	b.Grow(len(field) + 3)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			startsWord := i > 0 && (unicode.IsLower(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1])))
			if startsWord {
				b.WriteRune('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
