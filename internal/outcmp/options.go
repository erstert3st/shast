package outcmp

// Options controls how two outputs are compared.
type Options struct {
	// IgnoreOrder compares the outputs as multisets of lines, for commands
	// whose output order is not stable.
	IgnoreOrder bool
}
