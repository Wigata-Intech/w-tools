package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

var (
	errSelfConfig   = errors.New("the config-path flag cannot be set from the config file")
	errNotObject    = errors.New("config must be a JSON object")
	errTrailingData = errors.New("trailing data after config object")
	errValueType    = errors.New("value must be a string, number, or bool")
)

// ConfigFile enables config file support on a root Command. The named
// flag carries the file path; if the root does not declare it, cli
// declares it as a string flag with an empty default. A path set
// explicitly (flag or environment) must exist; a non-empty default path
// that does not exist is skipped.
type ConfigFile struct {
	// Flag is the flag name carrying the path, e.g. "config"; empty
	// disables config file support.
	Flag string

	// Decoder decodes the file into flag values. Nil means JSON: a flat
	// object keyed by flag name, with string, number, or bool values.
	Decoder Decoder
}

// Decoder decodes config file bytes into a flat map keyed by flag name.
// Values are strings and flow through flag.Value.Set exactly like
// command-line input. Format packages plug in here.
type Decoder func(data []byte) (map[string]string, error)

// loadConfig reads and decodes the root's config file, keyed off the
// already-resolved path flag.
func (d *dispatch) loadConfig(fromEnv map[string]bool) (map[string]string, error) {
	name := d.root.Config.Flag
	if name == "" {
		return map[string]string{}, nil
	}
	f := d.declared(d.root).inner.Lookup(name)
	file := f.Value.String()
	if file == "" {
		return map[string]string{}, nil
	}

	data, err := os.ReadFile(file) // #nosec G304 -- the operator's own config-path flag; reading it is the feature
	if err != nil {
		if os.IsNotExist(err) && !d.explicit[name] && !fromEnv[name] {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("cli: config file: %w", err)
	}

	decode := d.root.Config.Decoder
	if decode == nil {
		decode = decodeJSON
	}
	values, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("cli: config file %s: %w", file, err)
	}
	// Keys with no matching visible flag are ignored; only the
	// config-path flag itself is rejected.
	for key := range values {
		if key == name {
			return nil, fmt.Errorf("cli: config file %s: key %q: %w", file, key, errSelfConfig)
		}
	}
	return values, nil
}

// decodeJSON is the built-in Decoder: one flat JSON object, keys are flag
// names, values are strings, numbers, or bools.
func decodeJSON(data []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailingData
	}
	if raw == nil {
		return nil, errNotObject
	}
	values := make(map[string]string, len(raw))
	for key, v := range raw {
		switch t := v.(type) {
		case string:
			values[key] = t
		case json.Number:
			values[key] = t.String()
		case bool:
			values[key] = strconv.FormatBool(t)
		default:
			return nil, fmt.Errorf("key %q: %w", key, errValueType)
		}
	}
	return values, nil
}
