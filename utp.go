// Package utp is an implementation of the uTorrent Transport Protocol
package utp

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	mrand "math/rand"
	"net"
	"sync"
	"time"
)

type header struct {
	VerType  uint8
	Ex       bool
	ConnID   uint16
	Ts       uint32
	TsDelta  uint32
	WindowSz uint32
	SeqNum   uint16
	AckNum   uint16
	ExData   []byte
}

func (h *header) Len() int {
	return 20 + len(h.ExData)
}

func (h *header) Ser(b []byte) {
	b[0] = h.VerType
	if h.Ex {
		b[1] = 1
	}
	binary.BigEndian.PutUint16(b[2:3], h.ConnID)
	binary.BigEndian.PutUint32(b[4:7], h.Ts)
	binary.BigEndian.PutUint32(b[8:11], h.TsDelta)
	binary.BigEndian.PutUint32(b[12:15], h.WindowSz)
	binary.BigEndian.PutUint16(b[16:17], h.SeqNum)
	binary.BigEndian.PutUint16(b[18:19], h.AckNum)
}

func (h *header) Deser(b []byte) {
	h.VerType = uint8(b[0])
	h.Ex = (uint8(b[1]) != 0)
	h.ConnID = binary.BigEndian.Uint16(b[2:3])
	h.Ts = binary.BigEndian.Uint32(b[4:7])
	h.TsDelta = binary.BigEndian.Uint32(b[8:11])
	h.WindowSz = binary.BigEndian.Uint32(b[12:15])
	h.SeqNum = binary.BigEndian.Uint16(b[16:17])
	h.AckNum = binary.BigEndian.Uint16(b[18:19])
}

const (
	Data uint8 = iota
	Fin
	State
	Reset
	Syn

	controlTarget = uint32(100 * time.Millisecond)

	udp = "udp"
)

type connState uint8

const (
	csUninitialized connState = iota
	csIdle
	csSynSent
	csConnected
	csConnectedFull
	csGotFin
	csDestroyDelay
	csFinSent
	csReset
	csDestroy
)

var (
	maxWindowDecay = 500 * time.Microsecond
)

// a *Conn satisfies the net.Conn interface
type Conn struct {
	c    net.PacketConn
	prng *mrand.Rand

	// Number of packets in send queue (unsent and needing resend)
	// Oldest unacked packet is (seq - curWinPkts)
	curWinPkts uint16
	// amount of window used (bytes in-flight)
	curWin int
	// maximum window
	maxWin int
	// target delay in microseconds
	targetDelay int16
	// state of connection
	state     connState
	stateCond *sync.Cond
	// next packet to be sent
	seqNum uint16
	// packets recieved, inclusive
	ackNum uint16
	// time we last maxed the window
	lastMaxed time.Time
	// IDs
	recvID, sendID uint16
	// last window size we advertised
	lastRecvWin int

	// Delay histories
	ourHist, theirHist *hist

	// RTT stuff?
	rtt     uint32
	rttVar  uint32
	rto     uint32
	rttHist *hist

	// TODO(hank) MTU discovery

	pktSz               uint16
	rMicro              uint32
	rMutex              sync.Mutex
	winSz               uint16
	baseDelay, ourDelay uint32

	packets map[uint16]*packet
	resend  chan *packet
	ack     chan uint16
}

func Dial(address string) (*Conn, error) {
	ra, err := net.ResolveUDPAddr(udp, address)
	if err != nil {
		return nil, err
	}
	uc, err := net.DialUDP(udp, nil, ra)
	if err != nil {
		return nil, err
	}
	c, err := newConn(uc)
	if err != nil {
		return nil, err
	}
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func newConn(u net.PacketConn) (*Conn, error) {
	c := Conn{c: u}
	c.state = csIdle
	c.stateCond = sync.NewCond(&sync.Mutex{})
	b := make([]byte, 8)
	io.ReadFull(rand.Reader, b)
	c.packets = make(map[uint16]*packet)
	c.prng = mrand.New(mrand.NewSource(int64(binary.BigEndian.Uint64(b))))
	// these use a random-ish large number, since it can't be unbounded
	c.resend = make(chan *packet, 1024)
	c.ack = make(chan uint16, 1024)
	c.recvID = uint16(c.prng.Uint32() >> 16)
	c.sendID = c.recvID + 1
	c.curTime = time.Now()
	c.lastRecvd = c.curTime
	c.lastSent = c.curTime
	c.lastRWinDecay = c.curTime.Sub(maxWindowDecay)

	c.ourHist = &hist{}
	c.theirHist = &hist{}
	c.rttHist = &hist{}

	return &c, nil
}

func (c *Conn) connect() error {
	b := make([]byte, 20)
	c.seqNum = uint16(c.prng.Uint32() >> 16)

	p := &packet{
		Header: &header{
			VerType: Syn | 1<<4,
			SeqNum:  c.seqNum,
			// This is special to the SYN packet
			ConnID:   c.recvID,
			WindowSz: uint32(c.lastRecvWin),
			Ts:       uint32(time.Now().UnixNano() / int64(time.Millisecond)),
		},
		Tx: 0,
	}
	c.packets[p.Header.SeqNum] = p
	c.send(p)
	c.seqNum++
	c.curWinPkts++
	c.stateChange(csFinSent)

	c.stateCond.L.Lock()
	for c.state != csConnected {
		c.stateCond.Wait()
	}
	c.stateCond.L.Unlock()
	return nil
}

func (c *Conn) send(p *packet) {
	p.Lock()
	if p.Tx == 0 || p.NeedResend {
		c.curWin += p.Len()
	}
	p.NeedResend = false
	p.Header.AckNum = c.ackNum
	p.Sent = time.Now()

	// libutp has some mtu probing logic here
	// reproduce later?

	if _, err := c.c.WriteTo(p.Bytes(), c.remoteAddr); err != nil {
		debug("WriteTo error:", err)
		p.NeedResend = true
		c.resend <- p
	}
	p.Tx++
	p.Unlock()
}

func (c *Conn) unlink(id uint16) {
	c.packets[id].Lock()
	delete(c.packets, id)
	c.packets[id].Unlock()
}

// run spawns the receive and resend goroutines
func (c *Conn) run() {
	go func() {
		for p := range c.resend {
			c.send(p)
		}
	}()
	go func() {
		h := &header{}
		buf := make([]byte, 1500)
	MainLoop:
		for {
			select {
			case <-c.close:
				return
			default:
			}
			n, from, err := c.c.ReadFrom(buf)
			if n < 20 {
				debug("recv'd data can't contain a header, wat do?", buf[:n])
				continue
			}
			if err != nil {
				debug("ReadFrom error:", err)
				continue
			}
			if from.String() != c.remoteAddr {
				debug("martian packet:", from)
				continue
			}
			h.Deser(buf)
			switch h.Type {
			case Data:
				// queue up an ack
				// queue up resends
				// buffer payload for read
			case Fin:
				// notify everyone that we've got a fin
				c.stateChange(csGotFin)
				// close connection, wait for any outstanding acks
			case State:
				// Record acks
				switch c.state {
				case csUninitialized:
					debug("oh shi-")
					panic("connection in 'Uninitialized' state but received valid 'State' packet?")
				case csFinSent:
					// make sure this is the one we wanted
					// notify that we're connected
					if c.recvID != h.ConnID {
						debug("bogus syn-ack:", h)
						continue MainLoop
					}
					c.ackNum = h.SeqNum
					c.stateChange(csConnected)
				}
			case Reset:
				c.(net.PacketConn).Close()
				c.stateChange(csReset)
				return
			case Syn:
				// start connection
			}
		}
	}()
}
func (c *Conn) stateChange(s connState) {
	c.stateCond.L.Lock()
	c.state = s
	if s == csReset {
		close(c.close)
	}
	c.stateCond.L.Unlock()
	c.stateCond.Broadcast()
}

func (c *Conn) Read(b []byte) (int, error) {
}

func (c *Conn) Write(b []byte) (int, error) {
}

func (c *Conn) Close() error {
}

func (c *Conn) LocalAddr() net.Addr {
	return c.(*net.UDPConn).LocalAddr()
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.(*net.UDPConn).RemoteAddr()
}

func (c *Conn) SetDeadline(t time.Time) error {}

func (c *Conn) SetReadDeadline(t time.Time) error {}

func (c *Conn) SetWriteDeadline(t time.Time) error {}
