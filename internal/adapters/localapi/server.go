// Package localapi serves Feature 003's local control-plane API over a unix domain socket
// at $FOREMAN_HOME/state/foreman.sock (research.md R2). Every connection is checked against
// the daemon's own UID via LOCAL_PEERCRED before any request is read (spec FR-013); a
// mismatched peer gets the connection closed with no response, never an HTTP error body.
package localapi

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"golang.org/x/sys/unix"
)

// Server wraps a unix-socket listener with a per-connection peer-UID check.
type Server struct {
	listener *net.UnixListener
	handler  http.Handler
}

// New creates (removing any stale socket file first) a unix socket at path, mode 0700, and
// wraps handler to serve it. It does not start accepting connections until Serve is called.
func New(path string, handler http.Handler) (*Server, error) {
	_ = os.Remove(path) // a stale socket from a previous crash must not block a fresh bind

	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("resolve unix socket address: %w", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on unix socket: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod unix socket: %w", err)
	}
	return &Server{listener: ln, handler: handler}, nil
}

// Serve accepts connections until the listener is closed, checking each connection's peer
// UID before handing it to the HTTP server (FR-013). It blocks; run it in a goroutine.
func (s *Server) Serve() error {
	httpSrv := &http.Server{Handler: s.handler}
	return httpSrv.Serve(&peerCredListener{UnixListener: s.listener})
}

// Close stops accepting new connections and removes the socket file.
func (s *Server) Close() error {
	path := s.listener.Addr().String()
	err := s.listener.Close()
	_ = os.Remove(path)
	return err
}

// peerCredListener rejects a connection whose peer UID doesn't match this process's own UID
// before ever returning it to net/http — no HTTP response is ever written for a rejected
// peer, the connection is simply closed (FR-013).
type peerCredListener struct {
	*net.UnixListener
}

func (l *peerCredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.UnixListener.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if authorized(conn) {
			return conn, nil
		}
		conn.Close()
	}
}

func authorized(conn *net.UnixConn) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	var ok bool
	ctrlErr := raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			ok = false
			return
		}
		ok = AuthorizedUID(uint32(os.Getuid()), cred.Uid)
	})
	if ctrlErr != nil {
		return false
	}
	return ok
}

// AuthorizedUID reports whether peerUID may use the local API: it must match the daemon
// process's own UID (ownUID). Exported so tests can exercise the authorization rule itself
// without needing a second OS user.
func AuthorizedUID(ownUID, peerUID uint32) bool {
	return ownUID == peerUID
}
