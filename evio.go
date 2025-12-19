// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package evio is an event loop networking framework.
package evio

import (
	"fmt"
	"io"
	"iter"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Action is an action that occurs after the completion of an event.
type Action int

const (
	// None indicates that no action should occur following an event.
	None Action = iota
	// Detach detaches a connection. Not available for UDP connections.
	Detach
	// Close closes the connection.
	Close
	// Shutdown shutdowns the server.
	Shutdown
)

// Options are set when the client opens.
type Options struct {
	// TCPKeepAlive (SO_KEEPALIVE) socket option.
	TCPKeepAlive time.Duration
	// ReuseInputBuffer will forces the connection to share and reuse the
	// same input packet buffer with all other connections that also use
	// this option.
	// Default value is false, which means that all input data which is
	// passed to the Data event will be a uniquely copied []byte slice.
	ReuseInputBuffer bool
}

// ServerInfo represents a server context which provides information about the
// running server and has control functions for managing state.
type ServerInfo struct {
	// The addrs parameter is an array of listening addresses that align
	// with the addr strings passed to the Serve function.
	Addrs []net.Addr
	// NumLoops is the number of loops that the server is using.
	NumLoops int
}

// Conn is an evio connection.
type Conn interface {
	// Context returns a user-defined context.
	Context() any
	// SetContext sets a user-defined context.
	SetContext(any)
	// AddrIndex is the index of server address that was passed to the Serve call.
	AddrIndex() int
	// LocalAddr is the connection's local socket address.
	LocalAddr() net.Addr
	// RemoteAddr is the connection's remote peer address.
	RemoteAddr() net.Addr
	// Wake triggers a Data event for this connection.
	Wake()
}

// LoadBalance sets the load balancing method.
type LoadBalance int

const (
	// Random requests that connections are randomly distributed.
	Random LoadBalance = iota
	// RoundRobin requests that connections are distributed to a loop in a
	// round-robin fashion.
	RoundRobin
	// LeastConnections assigns the next accepted connection to the loop with
	// the least number of active connections.
	LeastConnections
)

// Events represents the server events for the Serve call.
// Each event has an Action return value that is used manage the state
// of the connection and server.
type Events struct {
	// NumLoops sets the number of loops to use for the server. Setting this
	// to a value greater than 1 will effectively make the server
	// multithreaded for multi-core machines. Which means you must take care
	// with synchonizing memory between all event callbacks. Setting to 0 or 1
	// will run the server single-threaded. Setting to -1 will automatically
	// assign this value equal to runtime.NumProcs().
	NumLoops int
	// LoadBalance sets the load balancing method. Load balancing is always a
	// best effort to attempt to distribute the incoming connections between
	// multiple loops. This option is only works when NumLoops is set.
	LoadBalance LoadBalance
	// Serving fires when the server can accept connections. The server
	// parameter has information and various utilities.
	OnServing func(server ServerInfo) (action Action)
	// Opened fires when a new connection has opened.
	// The info parameter has information about the connection such as
	// it's local and remote address.
	// Use the out return value to write data to the connection.
	// The opts return value is used to set connection options.
	OnOpened func(c Conn) (out []byte, opts Options, action Action)
	// Closed fires when a connection has closed.
	// The err parameter is the last known connection error.
	OnClosed func(c Conn, err error) (action Action)
	// Detached fires when a connection has been previously detached.
	// Once detached it's up to the receiver of this event to manage the
	// state of the connection. The Closed event will not be called for
	// this connection.
	// The conn parameter is a ReadWriteCloser that represents the
	// underlying socket connection. It can be freely used in goroutines
	// and should be closed when it's no longer needed.
	OnDetached func(c Conn, rwc io.ReadWriteCloser) (action Action)
	// PreWrite fires just before any data is written to any client socket.
	OnPreWrite func()
	// Data fires when a connection sends the server data.
	// The in parameter is the incoming data.
	// Use the out return value to write data to the connection.
	OnData func(c Conn, in []byte) (out []byte, action Action)
	// Tick fires immediately after the server starts and will fire again
	// following the duration specified by the delay return value.
	OnTick func() (delay time.Duration, action Action)
}

// Engine is an abstract Server definition
type Engine interface {
	Start()
	Stop()
	Serve() error
	Ready() chan bool
	Clear()
	HasErr() bool
	Errors() iter.Seq[error]
}

type serverBase struct {
	// 0: not running 1: running 2: auto exit 3: manually exit
	state     int32
	events    Events
	lns       []*listener
	cond      *sync.Cond // shutdown signaler
	ready     chan bool  // ensure server is ready
	stopped   chan bool  // ensure server is stopped
	errorMsgs chan error
	serr      error
}

func earlyQuit(events *Events, lns []*listener, numloops int, numlns int, state *int32) (confirm bool) {
	if events.OnServing != nil {
		var svr ServerInfo
		svr.NumLoops = numloops
		svr.Addrs = make([]net.Addr, numlns)
		for i, ln := range lns {
			svr.Addrs[i] = ln.lnaddr
		}

		action := events.OnServing(svr)
		switch action {
		case None:
		case Shutdown:
			confirm = true
			atomic.StoreInt32(state, 0)
		}
	}

	return
}

func (s *serverBase) bindListeners() error {
	var err error

	// bind listeners before run event loops
	for _, ln := range s.lns {
		if ln.network == "udp" {
			if ln.opts.reusePort {
				ln.pconn, err = reuseportListenPacket(ln.network, ln.addr)
			} else {
				ln.pconn, err = net.ListenPacket(ln.network, ln.addr)
			}
		} else {
			if ln.opts.reusePort {
				ln.ln, err = reuseportListen(ln.network, ln.addr)
			} else {
				ln.ln, err = net.Listen(ln.network, ln.addr)
			}
		}

		if err != nil {
			return err
		}

		if ln.pconn != nil {
			ln.lnaddr = ln.pconn.LocalAddr()
		} else {
			ln.lnaddr = ln.ln.Addr()
		}
	}

	return nil
}

func (s *serverBase) Ready() chan bool {
	return s.ready
}

func (s *serverBase) HasErr() bool {
	return len(s.errorMsgs) > 0
}

// Errors generator
func (s *serverBase) Errors() iter.Seq[error] {
	return func(yield func(error) bool) {
		for e := range s.errorMsgs {
			if !yield(e) {
				return
			}
		}
	}
}

// Clear server status and errors
func (s *serverBase) Clear() {
	st := atomic.LoadInt32(&s.state)
	switch st {
	case 0:
		return
	case 2:
		if _, ok := <-s.errorMsgs; ok {
			close(s.errorMsgs)
		}
		s.errorMsgs = make(chan error, 1<<10)
		atomic.StoreInt32(&s.state, 0)
		s.serr = nil
	}
}

// InputStream is a helper type for managing input streams from inside
// the Data event.
type InputStream struct{ b []byte }

// Begin accepts a new packet and returns a working sequence of
// unprocessed bytes.
func (is *InputStream) Begin(packet []byte) (data []byte) {
	data = packet
	if len(is.b) > 0 {
		is.b = append(is.b, data...)
		data = is.b
	}
	return data
}

// End shifts the stream to match the unprocessed data.
func (is *InputStream) End(data []byte) {
	if len(data) > 0 {
		if len(data) != len(is.b) {
			is.b = append(is.b[:0], data...)
		}
	} else if len(is.b) > 0 {
		is.b = is.b[:0]
	}
}

type listener struct {
	ln      net.Listener
	lnaddr  net.Addr
	pconn   net.PacketConn
	opts    addrOpts
	f       *os.File
	fd      int
	network string
	addr    string
}

type addrOpts struct {
	reusePort bool
}

var netSchemes = []string{"tcp", "tcp4", "tcp6", "udp", "udp4", "udp6", "unix"}

// NewEngine create a evloop server instance.
//
// # It run many event-loops to process network connection
//
// Addresses should use a scheme prefix and be formatted
// like `tcp://192.168.0.10:9851` or `unix://socket`.
// Valid network schemes:
//
//	tcp   - bind to both IPv4 and IPv6
//	tcp4  - IPv4
//	tcp6  - IPv6
//	udp   - bind to both IPv4 and IPv6
//	udp4  - IPv4
//	udp6  - IPv6
//	unix  - Unix Domain Socket
//
// The "tcp" network scheme is assumed when one is not specified.
func NewEngine(events Events, addrs ...string) (Engine, error) {
	var stdlib bool
	var lns []*listener

	for _, addr := range addrs {
		var ln listener
		var stdlibt bool
		ln.network, ln.addr, ln.opts, stdlibt = parseAddr(addr)
		if ln.addr == "" {
			return nil, fmt.Errorf("invalid address %s", addr)
		}

		if stdlibt {
			stdlib = true
		}
		if ln.network == "unix" {
			err := os.RemoveAll(ln.addr)
			if err != nil {
				return nil, err
			}
		}

		lns = append(lns, &ln)
	}

	if stdlib {
		return newStdServer(events, lns), nil
	}
	return newPollServer(events, lns), nil
}

func parseAddr(addr string) (network, address string, opts addrOpts, stdlib bool) {
	network = "tcp"
	opts.reusePort = false

	u, err := url.Parse(addr)
	if err != nil {
		address = ""
		return
	}

	schemes := strings.Split(u.Scheme, "-")
	if len(schemes) == 1 {
		stdlib = false
	}

	if len(schemes) == 2 && schemes[1] == "net" {
		stdlib = true
	}

	network = schemes[0]

	if !slices.Contains(netSchemes, network) {
		network = ""
		address = ""
		return
	}

	if u.Query().Has("reuseport") {
		val := u.Query().Get("reuseport")
		if len(val) > 0 {
			flagBit := val[0]
			if (flagBit >= '1' && flagBit <= '9') ||
				slices.Contains([]byte{'T', 't', 'Y', 'y'}, flagBit) {
				opts.reusePort = true
			}
		}
	}

	address = u.Host

	return
}
