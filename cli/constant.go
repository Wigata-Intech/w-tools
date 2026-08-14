package cli

// NoPrefix disables environment variable prefixing when set as a root
// Command's EnvPrefix: --http-addr binds to HTTP_ADDR. An empty EnvPrefix
// derives the prefix from the root command's name instead.
const NoPrefix = "-"
