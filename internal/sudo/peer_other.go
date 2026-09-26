//go:build !linux && !darwin

package sudo

import "net"

// peerCheckAvailable is false: outside Linux and macOS only the one-time token and
// the socket permissions protect the password.
const peerCheckAvailable = false

// peerIsSudo is not available outside Linux and macOS; the one-time token
// still applies. The sudo process is unknown (0).
func peerIsSudo(net.Conn) (int, error) { return 0, nil }
