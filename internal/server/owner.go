package server

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// ownerOnly closes connections that other users open. The browser's launch
// URL sits on its command line, where they can read it; on Linux the kernel
// also names the owner of each loopback socket, so a stolen link is useless.
// Root passes: it can read the books anyway. A socket missing from the
// tables, as without /proc/net, keeps only the token and one-time code as
// protection rather than locking out its owner.
type ownerOnly struct{ net.Listener }

func (l ownerOnly) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if uid, ok := peerUID(c); !ok || uid == os.Getuid() || uid == 0 {
			return c, nil
		}
		c.Close()
	}
}

// peerUID looks up the owner of the client socket of a loopback connection in
// /proc/net/tcp, or tcp6 for an IPv4-mapped client. ok is false where the
// tables do not exist, as outside Linux, or lack the socket. Tests replace it.
var peerUID = func(c net.Conn) (int, bool) {
	local, lok := c.LocalAddr().(*net.TCPAddr)
	remote, rok := c.RemoteAddr().(*net.TCPAddr)
	if !lok || !rok || local.IP.To4() == nil || remote.IP.To4() == nil {
		return 0, false
	}
	for _, table := range []struct{ path, prefix string }{
		{"/proc/net/tcp", ""},
		{"/proc/net/tcp6", mappedPrefix},
	} {
		data, err := os.ReadFile(table.path)
		if err != nil {
			continue
		}
		if uid, ok := socketUID(string(data), table.prefix+procAddr(remote), table.prefix+procAddr(local)); ok {
			return uid, true
		}
	}
	return 0, false
}

// mappedPrefix is the ::ffff: part of an IPv4-mapped address as tcp6 prints
// it: four host-order words.
var mappedPrefix = fmt.Sprintf("%016X%08X", 0, binary.NativeEndian.Uint32([]byte{0, 0, 0xff, 0xff}))

// procAddr formats an IPv4 address and port the way /proc/net/tcp does:
// the address as a host-order word, both in hex.
func procAddr(a *net.TCPAddr) string {
	return fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(a.IP.To4()), a.Port)
}

// socketUID finds the socket with the given local and remote addresses in a
// /proc/net/tcp table and returns its owner.
func socketUID(table, local, remote string) (int, bool) {
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) > 7 && f[1] == local && f[2] == remote {
			uid, err := strconv.Atoi(f[7])
			return uid, err == nil
		}
	}
	return 0, false
}
