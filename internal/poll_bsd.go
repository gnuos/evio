// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

//go:build darwin || netbsd || freebsd || openbsd || dragonfly
// +build darwin netbsd freebsd openbsd dragonfly

package internal

import (
	"syscall"
)

// Poll ...
type Poll struct {
	fd      int
	changes []syscall.Kevent_t
	notes   noteQueue
	done    chan bool
}

// OpenPoll ...
func OpenPoll() *Poll {
	l := new(Poll)
	p, err := syscall.Kqueue()
	if err != nil {
		panic(err)
	}

	l.fd = p
	_, err = syscall.Kevent(l.fd, []syscall.Kevent_t{{
		Ident:  0,
		Filter: syscall.EVFILT_USER,
		Flags:  syscall.EV_ADD | syscall.EV_CLEAR,
	}}, nil, nil)
	if err != nil {
		panic(err)
	}

	return l
}

// End ...
func (p *Poll) End() {
	p.done <- true
	close(p.done)
}

// Close ...
func (p *Poll) Close() error {
	return syscall.Close(p.fd)
}

// Trigger ...
func (p *Poll) Trigger(note any) error {
	p.notes.Add(note)
	_, err := syscall.Kevent(p.fd, []syscall.Kevent_t{{
		Ident:  0,
		Filter: syscall.EVFILT_USER,
		Fflags: syscall.NOTE_TRIGGER,
	}}, nil, nil)
	return err
}

// Wait ...
func (p *Poll) Wait(iter func(fd int, note any) error) error {
	events := make([]syscall.Kevent_t, 1024)
	for {
		select {
		case <-p.done:
			return nil
		default:
			{
				n, err := syscall.Kevent(p.fd, p.changes, events, nil)
				if err != nil && err != syscall.EINTR {
					return err
				}
				p.changes = p.changes[:0]
				if err := p.notes.ForEach(func(note any) error {
					return iter(0, note)
				}); err != nil {
					return err
				}
				for i := range n {
					if fd := int(events[i].Ident); fd != 0 {
						if err := iter(fd, nil); err != nil {
							return err
						}
					}
				}
			}
		}
	}
}

// AddRead ...
func (p *Poll) AddRead(fd int) {
	p.changes = append(p.changes,
		syscall.Kevent_t{
			Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ,
		},
	)
}

// AddReadWrite ...
func (p *Poll) AddReadWrite(fd int) {
	p.changes = append(p.changes,
		syscall.Kevent_t{
			Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ,
		},
		syscall.Kevent_t{
			Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE,
		},
	)
}

// ModRead ...
func (p *Poll) ModRead(fd int) {
	p.changes = append(p.changes, syscall.Kevent_t{
		Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE,
	})
}

// ModReadWrite ...
func (p *Poll) ModReadWrite(fd int) {
	p.changes = append(p.changes, syscall.Kevent_t{
		Ident: uint64(fd), Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE,
	})
}

// ModDetach ...
func (p *Poll) ModDetach(fd int) {
	p.changes = append(p.changes,
		syscall.Kevent_t{
			Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_READ,
		},
		syscall.Kevent_t{
			Ident: uint64(fd), Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE,
		},
	)
}
