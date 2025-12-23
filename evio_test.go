// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package evio

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServe(t *testing.T) {
	// start a server
	// connect 10 clients
	// each client will pipe random data for 1-3 seconds.
	// the writes to the server will be random sizes. 0KB - 1MB.
	// the server will echo back the data.
	// waits for graceful connection closing.
	t.Run("stdlib", func(t *testing.T) {
		t.Run("tcp", func(t *testing.T) {
			t.Run("1-loop", func(t *testing.T) {
				testServe("tcp-net", ":1997", false, 10, 1, Random)
			})
			t.Run("5-loop", func(t *testing.T) {
				testServe("tcp-net", ":1998", false, 10, 5, LeastConnections)
			})
			t.Run("N-loop", func(t *testing.T) {
				testServe("tcp-net", ":1999", false, 10, -1, RoundRobin)
			})
		})
		t.Run("unix", func(t *testing.T) {
			t.Run("1-loop", func(t *testing.T) {
				testServe("tcp-net", ":1989", true, 10, 1, Random)
			})
			t.Run("5-loop", func(t *testing.T) {
				testServe("tcp-net", ":1988", true, 10, 5, LeastConnections)
			})
			t.Run("N-loop", func(t *testing.T) {
				testServe("tcp-net", ":1987", true, 10, -1, RoundRobin)
			})
		})
	})
	t.Run("poll", func(t *testing.T) {
		t.Run("tcp", func(t *testing.T) {
			t.Run("1-loop", func(t *testing.T) {
				testServe("tcp", ":1991", false, 10, 1, Random)
			})
			t.Run("5-loop", func(t *testing.T) {
				testServe("tcp", ":1992", false, 10, 5, LeastConnections)
			})
			t.Run("N-loop", func(t *testing.T) {
				testServe("tcp", ":1993", false, 10, -1, RoundRobin)
			})
		})
		t.Run("unix", func(t *testing.T) {
			t.Run("1-loop", func(t *testing.T) {
				testServe("tcp", ":1994", true, 10, 1, Random)
			})
			t.Run("5-loop", func(t *testing.T) {
				testServe("tcp", ":1995", true, 10, 5, LeastConnections)
			})
			t.Run("N-loop", func(t *testing.T) {
				testServe("tcp", ":1996", true, 10, -1, RoundRobin)
			})
		})
	})

}

func testServe(network, addr string, unix bool, nclients, nloops int, balance LoadBalance) {
	var started int32
	var connected int32
	var disconnected int32

	var events Events
	events.LoadBalance = balance
	events.NumLoops = nloops
	events.OnServing = func(srv ServerInfo) (action Action) {
		return
	}
	events.OnOpened = func(c Conn) (out []byte, opts Options, action Action) {
		c.SetContext(c)
		atomic.AddInt32(&connected, 1)
		out = []byte("sweetness\r\n")
		opts.TCPKeepAlive = time.Minute * 5
		if c.LocalAddr() == nil {
			panic("nil local addr")
		}
		if c.RemoteAddr() == nil {
			panic("nil local addr")
		}
		return
	}
	events.OnClosed = func(c Conn, err error) (action Action) {
		if c.Context() != c {
			panic("invalid context")
		}
		atomic.AddInt32(&disconnected, 1)
		if atomic.LoadInt32(&connected) == atomic.LoadInt32(&disconnected) &&
			atomic.LoadInt32(&disconnected) == int32(nclients) {
			action = Shutdown
		}
		return
	}
	events.OnData = func(c Conn, in []byte) (out []byte, action Action) {
		out = in
		return
	}
	events.OnTick = func() (delay time.Duration, action Action) {
		if atomic.LoadInt32(&started) == 0 {
			for range nclients {
				go startClient(network, addr, nloops)
			}
			atomic.StoreInt32(&started, 1)
		}
		delay = time.Second / 5
		return
	}
	var err error
	var serv Engine
	if unix {
		socket := strings.Replace(addr, ":", "socket", 1)
		_ = os.RemoveAll(socket)
		defer func() {
			_ = os.RemoveAll(socket)
		}()
		serv, err = NewEngine(events, network+"://"+addr, "unix://"+socket)
	} else {
		serv, err = NewEngine(events, network+"://"+addr)
	}
	if err != nil {
		panic(err)
	}

	err = serv.Serve()
	if err != nil {
		panic(err)
	}
}

func startClient(network, addr string, nloops int) {
	onetwork := network
	network = strings.ReplaceAll(network, "-net", "")
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	c, err := net.Dial(network, addr)
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = c.Close()
	}()
	rd := bufio.NewReader(c)
	msg, err := rd.ReadBytes('\n')
	if err != nil {
		panic(err)
	}
	if string(msg) != "sweetness\r\n" {
		panic("bad header")
	}
	duration := time.Duration((rand.Float64()*2+1)*float64(time.Second)) / 8
	start := time.Now()
	for time.Since(start) < duration {
		sz := rand.Int() % (1024 * 1024)
		data := make([]byte, sz)
		if _, err := rng.Read(data); err != nil {
			panic(err)
		}
		if _, err := c.Write(data); err != nil {
			panic(err)
		}
		data2 := make([]byte, len(data))
		if _, err := io.ReadFull(rd, data2); err != nil {
			panic(err)
		}
		if string(data) != string(data2) {
			fmt.Printf("mismatch %s/%d: %d vs %d bytes\n", onetwork, nloops, len(data), len(data2))
			//panic("mismatch")
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func TestTick(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("tcp", ":2991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("tcp", ":2992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testTick("unix", "socket2", true)
	}()
	wg.Wait()
}

func testTick(network, addr string, stdlib bool) {
	var events Events
	var count int
	start := time.Now()
	events.OnTick = func() (delay time.Duration, action Action) {
		if count == 25 {
			action = Shutdown
			return
		}
		count++
		delay = time.Millisecond * 10
		return
	}

	var listen string
	if stdlib {
		listen = network + "-net://" + addr
	} else {
		listen = network + "://" + addr
	}

	serv, err := NewEngine(events, listen)
	must(err)
	must(serv.Serve())

	dur := time.Since(start)
	if dur < 250&time.Millisecond || dur > time.Second {
		panic("bad ticker timing")
	}
}

func TestShutdown(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("tcp", ":3991", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("tcp", ":3992", true)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("unix", "socket1", false)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		testShutdown("unix", "socket2", true)
	}()
	wg.Wait()
}
func testShutdown(network, addr string, stdlib bool) {
	var events Events
	var count int
	var clients int64
	var N = 10
	events.OnOpened = func(c Conn) (out []byte, opts Options, action Action) {
		atomic.AddInt64(&clients, 1)
		return
	}
	events.OnClosed = func(c Conn, err error) (action Action) {
		atomic.AddInt64(&clients, -1)
		return
	}
	events.OnTick = func() (delay time.Duration, action Action) {
		if count == 0 {
			// start clients
			for range N {
				go func() {
					conn, err := net.Dial(network, addr)
					must(err)
					defer func() {
						_ = conn.Close()
					}()
					_, err = conn.Read([]byte{0})
					if err == nil {
						panic("expected error")
					}
				}()
			}
		} else {
			if int(atomic.LoadInt64(&clients)) == N {
				action = Shutdown
			}
		}
		count++
		delay = time.Second / 20
		return
	}

	var listen string
	if stdlib {
		listen = network + "-net://" + addr
	} else {
		listen = network + "://" + addr
	}

	serv, err := NewEngine(events, listen)
	must(err)
	must(serv.Serve())

	if clients != 0 {
		panic("did not call close on all clients")
	}
}

func TestDetach(t *testing.T) {
	t.Run("poll", func(t *testing.T) {
		t.Run("tcp", func(t *testing.T) {
			testDetach("tcp", ":4991", false)
		})
		t.Run("unix", func(t *testing.T) {
			testDetach("unix", "socket1", false)
		})
	})
	t.Run("stdlib", func(t *testing.T) {
		t.Run("tcp", func(t *testing.T) {
			testDetach("tcp", ":4992", true)
		})
		t.Run("unix", func(t *testing.T) {
			testDetach("unix", "socket2", true)
		})
	})
}

func testDetach(network, addr string, stdlib bool) {
	// we will write a bunch of data with the text "--detached--" in the
	// middle followed by a bunch of data.
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	rdat := make([]byte, 10*1024)
	if _, err := rng.Read(rdat); err != nil {
		panic("random error: " + err.Error())
	}
	expected := []byte(string(rdat) + "--detached--" + string(rdat))
	var cin []byte
	var events Events
	events.OnData = func(c Conn, in []byte) (out []byte, action Action) {
		cin = append(cin, in...)
		if len(cin) >= len(expected) {
			if string(cin) != string(expected) {
				panic("mismatch client -> server")
			}
			return cin, Detach
		}
		return
	}

	var done int64
	events.OnDetached = func(c Conn, conn io.ReadWriteCloser) (action Action) {
		go func() {
			p := make([]byte, len(expected))
			defer func() {
				_ = conn.Close()
			}()
			_, err := io.ReadFull(conn, p)
			must(err)
			_, _ = conn.Write(expected)
		}()
		return
	}

	events.OnServing = func(srv ServerInfo) (action Action) {
		go func() {
			p := make([]byte, len(expected))
			_ = expected
			conn, err := net.Dial(network, addr)
			must(err)
			defer func() {
				_ = conn.Close()
			}()
			_, _ = conn.Write(expected)
			_, err = io.ReadFull(conn, p)
			must(err)
			_, _ = conn.Write(expected)
			_, err = io.ReadFull(conn, p)
			must(err)
			atomic.StoreInt64(&done, 1)
		}()
		return
	}
	events.OnTick = func() (delay time.Duration, action Action) {
		delay = time.Second / 5
		if atomic.LoadInt64(&done) == 1 {
			action = Shutdown
		}
		return
	}

	var listen string
	if stdlib {
		listen = network + "-net://" + addr
	} else {
		listen = network + "://" + addr
	}

	serv, err := NewEngine(events, listen)
	must(err)
	must(serv.Serve())
}

func TestBadAddresses(t *testing.T) {
	var events Events
	events.OnServing = func(srv ServerInfo) (action Action) {
		return Shutdown
	}

	if _, err := NewEngine(events, "tulip://howdy"); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := NewEngine(events, "howdy"); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := NewEngine(events, "tcp://"); err == nil {
		t.Fatalf("expected nil, got '%v'", err)
	}
}

func TestInputStream(t *testing.T) {
	var s InputStream
	in := []byte("HELLO")
	data := s.Begin(in)
	if string(data) != string(in) {
		t.Fatalf("expected '%v', got '%v'", in, data)
	}
	s.End(in[3:])
	data = s.Begin([]byte("WLY"))
	if string(data) != "LOWLY" {
		t.Fatalf("expected '%v', got '%v'", "LOWLY", data)
	}
	s.End(nil)
	data = s.Begin([]byte("PLAYER"))
	if string(data) != "PLAYER" {
		t.Fatalf("expected '%v', got '%v'", "PLAYER", data)
	}
}

func TestReuseInputBuffer(t *testing.T) {
	reuses := []bool{true, false}
	t.Run("reuseBuffer", func(t *testing.T) {
		t.Run(fmt.Sprintf("%v", reuses[0]), func(t *testing.T) {
			var events Events
			events.OnOpened = func(c Conn) (out []byte, opts Options, action Action) {
				opts.ReuseInputBuffer = reuses[0]
				return
			}
			var prev []byte
			events.OnData = func(c Conn, in []byte) (out []byte, action Action) {
				if prev == nil {
					prev = in
				} else {
					reused := string(in) == string(prev)
					if reused != reuses[0] {
						t.Fatalf("expected %v, got %v", reuses[0], reused)
					}
					action = Shutdown
				}
				return
			}
			events.OnServing = func(_ ServerInfo) (action Action) {
				go func() {
					c, err := net.Dial("tcp", ":5991")
					must(err)
					defer func() {
						_ = c.Close()
					}()
					_, _ = c.Write([]byte("packet1"))
					time.Sleep(time.Second / 5)
					_, _ = c.Write([]byte("packet2"))
				}()
				return
			}

			serv, err := NewEngine(events, "tcp://:5991")
			must(err)
			must(serv.Serve())
		})
		t.Run(fmt.Sprintf("%v", reuses[1]), func(t *testing.T) {
			var events Events
			events.OnOpened = func(c Conn) (out []byte, opts Options, action Action) {
				opts.ReuseInputBuffer = reuses[1]
				return
			}
			var prev []byte
			events.OnData = func(c Conn, in []byte) (out []byte, action Action) {
				if prev == nil {
					prev = in
				} else {
					reused := string(in) == string(prev)
					if reused != reuses[1] {
						t.Fatalf("expected %v, got %v", reuses[1], reused)
					}
					action = Shutdown
				}
				return
			}
			events.OnServing = func(_ ServerInfo) (action Action) {
				go func() {
					c, err := net.Dial("tcp", ":5992")
					must(err)
					defer func() {
						_ = c.Close()
					}()
					_, _ = c.Write([]byte("packet1"))
					time.Sleep(time.Second / 5)
					_, _ = c.Write([]byte("packet2"))
				}()
				return
			}
			serv, err := NewEngine(events, "tcp://:5992")
			must(err)
			must(serv.Serve())
		})
	})
}

func TestReuseport(t *testing.T) {
	var events Events
	events.OnServing = func(s ServerInfo) (action Action) {
		return Shutdown
	}

	var wg sync.WaitGroup
	wg.Add(5)
	for i := range 5 {
		var v = "1"
		if i%2 == 0 {
			v = "true"
		}
		go func(v string) {
			defer wg.Done()

			serv, err := NewEngine(events, "tcp://:9991?reuseport="+v)
			if serv == nil {
				t.Error(err)
				return
			}
			must(err)
			must(serv.Serve())
		}(v)
	}

	wg.Wait()
}
