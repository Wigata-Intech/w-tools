// Package cli is a zero-dependency command framework: a command tree
// dispatched from os.Args, flags with environment binding, layered
// configuration, and generated help.
//
// Commands compose into a tree and dispatch context-first; handlers are
// plain funcs. Flags declared on a command are visible to it and all of
// its descendants, and every flag resolves with one fixed precedence:
//
//	explicit flag > environment variable > *_FILE indirection > config file > default
//
// Environment names derive mechanically from flag names — with prefix
// MY_SERVICE, --http-addr binds to MY_SERVICE_HTTP_ADDR and
// MY_SERVICE_HTTP_ADDR_FILE — so the mapping is never documented by
// hand. The prefix is per-binary, derived from the root command's name,
// overridable via EnvPrefix, and disabled with NoPrefix.
//
// Config files are JSON (the stdlib format); other formats plug in
// through the Decoder seam without cli growing parsers.
//
// Exit codes follow convention: 0 success, 1 a command's Run returned an
// error, 2 usage, environment, or configuration error. Help and version
// output go to stdout; errors go to stderr.
package cli
