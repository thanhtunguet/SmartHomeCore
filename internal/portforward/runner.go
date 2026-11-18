package portforward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Limits of one running rule. They keep a rule that is flooded (or pointed at a dead target) from
// exhausting the file descriptors and memory of a small Controller host.
const (
	dialTimeout    = 5 * time.Second
	tcpKeepAlive   = 30 * time.Second
	maxTCPConns    = 512              // concurrent relayed TCP connections; more are closed at once
	maxUDPSessions = 256              // concurrent UDP clients; datagrams of more are dropped
	udpIdle        = 60 * time.Second // a UDP client with no traffic either way is forgotten
	udpBufSize     = 64 * 1024        // largest UDP datagram
	acceptBackoff  = 100 * time.Millisecond
)

// runner holds the open listeners of one rule and the connections it relays. Stop closes all of
// them and waits for every goroutine.
type runner struct {
	rule   Rule
	target string // host:port, resolved on every dial
	log    *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	closers   []io.Closer // listeners
	conns     map[net.Conn]struct{}
	stopped   bool
	tcpSlots  chan struct{}
	udpActive atomic.Int64
}

// networks returns the net package network names to bind rule's listen address: an IPv4 address is
// bound with the "4" networks (so 0.0.0.0 stays IPv4 only, as written), the IPv6 wildcard with the
// plain ones (dual stack) and any other IPv6 address with the "6" networks.
func networks(listen string) (tcp, udp string) {
	ip, _ := netip.ParseAddr(listen)
	switch {
	case ip.Is4():
		return "tcp4", "udp4"
	case ip.IsUnspecified():
		return "tcp", "udp"
	default:
		return "tcp6", "udp6"
	}
}

// start opens the listeners of rule and begins relaying. It returns once the sockets are bound, so
// a bind error (port in use, address not on this host, no privilege) is reported to the caller; if
// any listener of a universal rule fails, none stays open.
func start(parent context.Context, rule Rule, log *slog.Logger) (*runner, error) {
	ctx, cancel := context.WithCancel(parent)
	r := &runner{
		rule: rule, log: log.With("rule", rule.Name, "rule_id", rule.ID),
		target: net.JoinHostPort(rule.TargetHost, strconv.Itoa(rule.TargetPort)),
		ctx:    ctx, cancel: cancel, conns: map[net.Conn]struct{}{}, tcpSlots: make(chan struct{}, maxTCPConns),
	}
	listen := net.JoinHostPort(rule.ListenAddress, strconv.Itoa(rule.ListenPort))
	tcpNet, udpNet := networks(rule.ListenAddress)
	var lc net.ListenConfig

	var ln net.Listener
	var pc net.PacketConn
	var err error
	if rule.Protocol.HasTCP() {
		if ln, err = lc.Listen(ctx, tcpNet, listen); err != nil {
			cancel()
			return nil, err
		}
		r.closers = append(r.closers, ln)
	}
	if rule.Protocol.HasUDP() {
		if pc, err = lc.ListenPacket(ctx, udpNet, listen); err != nil {
			for _, c := range r.closers {
				_ = c.Close()
			}
			cancel()
			return nil, err
		}
		r.closers = append(r.closers, pc)
	}
	if ln != nil {
		r.wg.Go(func() { r.serveTCP(ln) })
	}
	if pc != nil {
		r.wg.Go(func() { r.serveUDP(pc.(*net.UDPConn)) })
	}
	r.log.Info("port forwarding started", "protocol", rule.Protocol, "listen", listen, "target", r.target)
	return r, nil
}

// stop closes the listeners and every relayed connection and waits for the goroutines to finish.
func (r *runner) stop() {
	r.cancel()
	r.mu.Lock()
	r.stopped = true
	for _, c := range r.closers {
		_ = c.Close()
	}
	for c := range r.conns {
		_ = c.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
	r.log.Info("port forwarding stopped")
}

// track registers c so stop can close it; false if the runner is already stopping.
func (r *runner) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

func (r *runner) untrack(c net.Conn) {
	r.mu.Lock()
	delete(r.conns, c)
	r.mu.Unlock()
}

// ---- TCP ----------------------------------------------------------------------------------------

func (r *runner) serveTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || r.ctx.Err() != nil {
				return
			}
			r.log.Warn("tcp accept failed", "err", err)
			select { // e.g. out of file descriptors: do not spin
			case <-time.After(acceptBackoff):
			case <-r.ctx.Done():
				return
			}
			continue
		}
		select {
		case r.tcpSlots <- struct{}{}:
			r.wg.Go(func() {
				defer func() { <-r.tcpSlots }()
				r.relayTCP(c)
			})
		default:
			r.log.Warn("tcp connection refused: too many connections", "limit", maxTCPConns)
			_ = c.Close()
		}
	}
}

func (r *runner) relayTCP(client net.Conn) {
	defer client.Close()
	if !r.track(client) {
		return
	}
	defer r.untrack(client)

	d := net.Dialer{Timeout: dialTimeout, KeepAlive: tcpKeepAlive}
	up, err := d.DialContext(r.ctx, "tcp", r.target)
	if err != nil {
		r.log.Debug("tcp dial failed", "err", err)
		return
	}
	defer up.Close()
	if !r.track(up) {
		return
	}
	defer r.untrack(up)

	// Each direction copies until its source ends, then half-closes the destination so the peer
	// still sees the stream end but can finish its own reply.
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go pipe(up, client)
	go pipe(client, up)
	<-done
	<-done
}

// ---- UDP ----------------------------------------------------------------------------------------

// udpSession is the upstream socket of one client address. UDP has no connections, so a client is
// remembered (its own upstream socket, so replies find their way back) until it is silent for udpIdle.
type udpSession struct {
	up   *net.UDPConn
	last atomic.Int64 // unix nanoseconds of the last datagram in either direction
}

func (s *udpSession) touch() { s.last.Store(time.Now().UnixNano()) }

func (s *udpSession) idle() time.Duration { return time.Since(time.Unix(0, s.last.Load())) }

func (r *runner) serveUDP(pc *net.UDPConn) {
	var (
		mu       sync.Mutex
		sessions = map[netip.AddrPort]*udpSession{}
	)
	forget := func(from netip.AddrPort, s *udpSession) {
		mu.Lock()
		if sessions[from] == s {
			delete(sessions, from)
		}
		mu.Unlock()
		_ = s.up.Close()
		r.untrack(s.up)
		r.udpActive.Add(-1)
	}
	buf := make([]byte, udpBufSize)
	for {
		n, from, err := pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || r.ctx.Err() != nil {
				return
			}
			r.log.Debug("udp read failed", "err", err)
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())

		mu.Lock()
		s := sessions[from]
		if s == nil {
			if len(sessions) >= maxUDPSessions {
				mu.Unlock()
				r.log.Debug("udp datagram dropped: too many clients", "limit", maxUDPSessions)
				continue
			}
			s = r.newUDPSession()
			if s == nil {
				mu.Unlock()
				continue
			}
			sessions[from] = s
			r.udpActive.Add(1)
			r.wg.Go(func() {
				r.udpReplies(pc, from, s)
				forget(from, s)
			})
		}
		mu.Unlock()
		s.touch()
		if _, err := s.up.Write(buf[:n]); err != nil {
			r.log.Debug("udp write to target failed", "err", err)
		}
	}
}

// newUDPSession opens the upstream socket for a new client; nil (already logged) if it cannot.
func (r *runner) newUDPSession() *udpSession {
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(r.ctx, "udp", r.target)
	if err != nil {
		r.log.Debug("udp dial failed", "err", err)
		return nil
	}
	up := c.(*net.UDPConn)
	if !r.track(up) {
		_ = up.Close()
		return nil
	}
	s := &udpSession{up: up}
	s.touch()
	return s
}

// udpReplies copies the target's answers back to the client through the listening socket, until the
// client has been silent for udpIdle or the rule stops.
func (r *runner) udpReplies(pc *net.UDPConn, from netip.AddrPort, s *udpSession) {
	buf := make([]byte, udpBufSize)
	for {
		_ = s.up.SetReadDeadline(time.Now().Add(udpIdle))
		n, err := s.up.Read(buf)
		if n > 0 {
			s.touch()
			if _, werr := pc.WriteToUDPAddrPort(buf[:n], from); werr != nil && r.ctx.Err() != nil {
				return
			}
			continue
		}
		var ne net.Error
		switch {
		case errors.Is(err, net.ErrClosed) || r.ctx.Err() != nil:
			return
		case errors.As(err, &ne) && ne.Timeout():
			if s.idle() >= udpIdle {
				return
			}
		default:
			// A connected UDP socket reports an ICMP "port unreachable" from the target as a read
			// error, once per datagram sent. The client simply gets no answer; keep the session.
			if s.idle() >= udpIdle {
				return
			}
		}
	}
}
