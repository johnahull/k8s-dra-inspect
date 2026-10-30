package snapshot

// SetMaxBytes lowers the size cap for a test and returns a restore func.
func SetMaxBytes(n int64) func() {
	old := maxBytes
	maxBytes = n
	return func() { maxBytes = old }
}
