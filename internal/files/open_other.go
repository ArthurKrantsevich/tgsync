//go:build !unix

package files

// openFlags: there are no FIFOs to guard against here; a swapped file is
// caught by the identity check.
const openFlags = 0
