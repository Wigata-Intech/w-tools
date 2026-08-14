package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// writeHelp generates the help text for the command at the end of path:
// description, usage line, subcommand list, and the visible flag table
// with each flag's environment name and default.
func writeHelp(w io.Writer, d *dispatch, path []*Command) {
	cmd := path[len(path)-1]

	if desc := longDesc(cmd); desc != "" {
		_, _ = fmt.Fprintf(w, "%s\n\n", desc)
	}

	_, _ = fmt.Fprintf(w, "Usage:\n  %s%s%s\n", pathName(path), usageCommands(cmd), usageFlags(d, path))

	if entries := commandEntries(d.root, path); len(entries) > 0 {
		_, _ = fmt.Fprintf(w, "\nCommands:\n")
		writeAligned(w, entries)
	}

	if entries := flagEntries(d, path); len(entries) > 0 {
		_, _ = fmt.Fprintf(w, "\nFlags:\n")
		writeAligned(w, entries)
	}
}

func longDesc(cmd *Command) string {
	if cmd.Long != "" {
		return cmd.Long
	}
	return cmd.Short
}

func usageCommands(cmd *Command) string {
	if len(cmd.Commands) > 0 {
		return " [command]"
	}
	return ""
}

func usageFlags(d *dispatch, path []*Command) string {
	has := false
	d.visible(path, func(*FlagSet, *flag.Flag) { has = true })
	if has {
		return " [flags]"
	}
	return ""
}

type helpEntry struct {
	head string
	desc string
}

// commandEntries lists cmd's subcommands, plus the help and version
// built-ins on the root.
func commandEntries(root *Command, path []*Command) []helpEntry {
	cmd := path[len(path)-1]
	entries := make([]helpEntry, 0, len(cmd.Commands)+2)
	for _, sub := range cmd.Commands {
		entries = append(entries, helpEntry{head: sub.Name, desc: sub.Short})
	}
	if cmd == root && len(cmd.Commands) > 0 {
		if findCommand(cmd.Commands, "help") == nil {
			entries = append(entries, helpEntry{head: "help", desc: "help for " + root.Name + " or a command"})
		}
		if root.Version != "" && findCommand(cmd.Commands, "version") == nil {
			entries = append(entries, helpEntry{head: "version", desc: "print the version"})
		}
	}
	return entries
}

// flagEntries renders the visible flags: -name and type, then usage, env
// name, and default. Secret flags mask their default.
func flagEntries(d *dispatch, path []*Command) []helpEntry {
	prefix := envPrefix(d.root)
	var entries []helpEntry
	d.visible(path, func(owner *FlagSet, f *flag.Flag) {
		typ, usage := flag.UnquoteUsage(f)
		head := "-" + f.Name
		if typ != "" {
			head += " " + typ
		}
		desc := usage
		if owner.required[f.Name] {
			desc += " (required)"
		}
		desc += " (env " + envName(prefix, f.Name) + ")"
		if def := defaultText(owner, f); def != "" {
			desc += " (default " + def + ")"
		}
		entries = append(entries, helpEntry{head: head, desc: strings.TrimSpace(desc)})
	})
	return entries
}

// defaultText returns the rendered default, empty for the declaring
// type's zero value — mirroring flag.PrintDefaults — and masked for
// secret flags.
func defaultText(owner *FlagSet, f *flag.Flag) string {
	if owner.zero[f.Name] {
		return ""
	}
	if owner.secret[f.Name] {
		return "<secret>"
	}
	return fmt.Sprintf("%q", f.DefValue)
}

// writeAligned prints "  head  desc" rows with heads padded to one width.
func writeAligned(w io.Writer, entries []helpEntry) {
	width := 0
	for _, e := range entries {
		if len(e.head) > width {
			width = len(e.head)
		}
	}
	for _, e := range entries {
		if e.desc == "" {
			_, _ = fmt.Fprintf(w, "  %s\n", e.head)
			continue
		}
		_, _ = fmt.Fprintf(w, "  %-*s  %s\n", width, e.head, e.desc)
	}
}
