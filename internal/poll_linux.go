// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package internal

import (
	"syscall"
	"unsafe"
)

// Poll ...
type Poll struct {
	fd    int // epoll fd
	wfd   int // wake fd
	notes noteQueue
	done  chan bool
}

// OpenPoll ...
func OpenPoll() *Poll {
	l := new(Poll)
	p, err := syscall.EpollCreate1(0)
	if err != nil {
		panic(err)
	}

	l.fd = p
	r0, _, e0 := syscall.Syscall(syscall.SYS_EVENTFD2, 0, 0, 0)
	if e0 != 0 {
		err = syscall.Close(p)
		panic(err)
	}
	l.wfd = int(r0)
	l.AddRead(l.wfd)
	l.done = make(chan bool, 1)

	return l
}

// End
func (p *Poll) End() {
	p.done <- true
	close(p.done)
}

// Close ...
func (p *Poll) Close() error {
	if err := syscall.Close(p.wfd); err != nil {
		return err
	}
	return syscall.Close(p.fd)
}

// Trigger ...
func (p *Poll) Trigger(note any) error {
	p.notes.Add(note)
	var x uint64 = 1
	_, err := syscall.Write(p.wfd, (*(*[8]byte)(unsafe.Pointer(&x)))[:])
	return err
}

// Wait ...
func (p *Poll) Wait(iter func(fd int, note any) error) error {
	events := make([]syscall.EpollEvent, 64)
	for {
		select {
		case <-p.done:
			return nil
		default:
			{
				n, err := syscall.EpollWait(p.fd, events, 100)
				if err != nil && err != syscall.EINTR {
					return err
				}
				if err := p.notes.ForEach(func(note any) error {
					return iter(0, note)
				}); err != nil {
					return err
				}
				for i := range n {
					if fd := int(events[i].Fd); fd != p.wfd {
						if err := iter(fd, nil); err != nil {
							return err
						}
					} else if fd == p.wfd {
						var data [8]byte
						_, _ = syscall.Read(p.wfd, data[:])
					}
				}
			}
		}
	}
}

// AddReadWrite ...
func (p *Poll) AddReadWrite(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_ADD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		},
	); err != nil {
		panic(err)
	}
}

// AddRead ...
func (p *Poll) AddRead(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_ADD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		},
	); err != nil {
		panic(err)
	}
}

// ModRead ...
func (p *Poll) ModRead(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_MOD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		},
	); err != nil {
		panic(err)
	}
}

// ModReadWrite ...
func (p *Poll) ModReadWrite(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_MOD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		},
	); err != nil {
		panic(err)
	}
}

// ModDetach ...
func (p *Poll) ModDetach(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_DEL, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		},
	); err != nil {
		panic(err)
	}
}
