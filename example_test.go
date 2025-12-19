package evio

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

func ExampleEngine() {
	var (
		port       = 6399
		unixsocket = "example-test.sock"
	)

	var (
		events Events
	)

	events.NumLoops = 2
	events.OnServing = func(srv ServerInfo) (action Action) {
		// log.Printf("echo server started on port %d (loops: %d)", port, srv.NumLoops)
		if unixsocket != "" {
			// log.Printf("echo server started at %s (loops: %d)", unixsocket, srv.NumLoops)
		}
		return
	}

	events.OnOpened = func(ec Conn) (out []byte, opts Options, action Action) {
		// log.Printf("opened: %v", ec.RemoteAddr())
		ec.SetContext((*InputStream)(nil))
		opts.ReuseInputBuffer = true
		opts.TCPKeepAlive = 30 * time.Second
		return
	}

	events.OnClosed = func(ec Conn, err error) (action Action) {
		// log.Printf("closed: %v", ec.RemoteAddr())
		return
	}

	events.OnData = makeHandler()

	addrs := []string{fmt.Sprintf("tcp://:%d", port)}
	if unixsocket != "" {
		addrs = append(addrs, fmt.Sprintf("unix://%s", unixsocket))
	}

	var err error
	var c net.Conn

	serv, err := NewEngine(events, addrs...)
	if err != nil {
		log.Fatal(err)
	}

	serv.Start()

	<-serv.Ready()

	if unixsocket != "" {
		c, err = net.Dial("unix", unixsocket)
	} else {
		c, err = net.Dial("tcp", "127.0.0.1:6399")
	}

	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		_ = c.Close()
		serv.Stop()
		serv.Clear()
		_ = os.Remove(unixsocket)
	}()

	_, err = c.Write([]byte("GET"))
	if err != nil {
		log.Println(err)
		return
	}

	var b = make([]byte, 128)

	_, err = c.Read(b)
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Printf("%s", strings.TrimSpace(string(b)))
}

func makeHandler() func(Conn, []byte) ([]byte, Action) {
	return func(ec Conn, in []byte) (out []byte, action Action) {
		if in == nil {
			log.Printf("wake from %s\n", ec.RemoteAddr())
			return nil, Close
		}
		ec.SetContext((*InputStream)(nil))

		out = in
		return
	}
}
