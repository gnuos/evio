// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build !darwin && !netbsd && !freebsd && !openbsd && !dragonfly && !linux
// +build !darwin,!netbsd,!freebsd,!openbsd,!dragonfly,!linux

package evio

import (
	"errors"
	"net"
	"os"
)

func (ln *listener) close() {
	if ln.ln != nil {
		_ = ln.ln.Close()
	}
	if ln.pconn != nil {
		_ = ln.pconn.Close()
	}
	if ln.network == "unix" {
		_ = os.RemoveAll(ln.addr)
	}
}

func (ln *listener) system() error {
	return nil
}

func reuseportListenPacket(_, _ string) (l net.PacketConn, err error) {
	return nil, errors.New("reuseport is not available")
}

func reuseportListen(_, _ string) (l net.Listener, err error) {
	return nil, errors.New("reuseport is not available")
}
