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
	Uninitialized connState = iota
	SynSent
	Connected
)

var (
	rng *mrand.Rand
)

func init() {
	b := make([]byte, 8)
	io.ReadFull(rand.Reader, b)
	rng = mrand.New(mrand.NewSource(int64(binary.BigEndian.Uint64(b))))
}

// a *Conn satisfies the net.Conn interface
type Conn struct {
	*net.UDPConn

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
	state connState
	// next packet to be sent
	seq uint16
	// packets recieved, inclusive
	ack uint16
	// time we last maxed the window
	lastMaxed time.Time
	// IDs
	recvID, sendID uint32
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

	snd, ack *sync.Cond
	register *sync.RWMutex
	packets  map[uint16][]byte

	out packetBuf
	in  packetBuf
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
	// we'll do all buffering, try to disable the OS's buffers
	if err := uc.SetReadBuffer(0); err != nil {
		return nil, err
	}
	if err := uc.SetWriteBuffer(0); err != nil {
		return nil, err
	}
	c := &Conn{uc}
	c.snd = sync.NewCond(&sync.Mutex{})
	c.ack = sync.NewCond(&sync.Mutex{})
	c.packets = make(map[uint16][]byte)
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Conn) connect() error {
	b := make([]byte, 20)
	c.seq = 1
	c.recvID = rng.Uint32()
	c.sendID = c.recvID + 1
	p := &packet{
		Header: &header{
			VerType:  Syn | 1<<4,
			SeqNum:   c.seq,
			ConnID:   c.recvID,
			WindowSz: uint32(c.lastRecvWin),
			Ts:       uint32(time.Now().UnixNano() / int64(time.Millisecond)),
		},
	}
	c.out.Ensure(c.seq, c.curWinPkts)
	c.out.Put(c.seq, p)
	c.seq++
	c.curWinPkts++
	c.send(p)
	return nil
}

func (c *Conn) send(p *packet) {
	if p.Tx == 0 || p.NeedResend {
		c.curWin += len(p.Payload)
	}
	p.NeedResend = false
	p.Header.AckNum = c.ackNum
	p.Sent = time.Now()

	// libutp has some mtu probing logic here
	// reproduce later?

	c.(*net.UDPConn).WriteMsgUDP(p.Bytes(), ???, ???)
}

/*
func (c *Conn) send(data []byte) error {
	l := len(data)

	// block until we're allowed to put more bytes in flight
	c.snd.L.Lock()
	if c.curWin+c.pktSz > minU16(c.maxWin, c.winSz) {
		c.snd.Wait()
	}

	// determine the number of packets we'll need
	n := l / c.pktSz
	if l%c.pktSz != 0 {
		n++
	}
	w := make([]uint16, n)

	// claim the sequence numbers
	c.register.Lock()
	for i := 0; i < n; i++ {
		seq := i + 1
		off := i * c.pktSz
		// put in the queue
		s.packets[seq] = data[off:minU16(x.pktSz, l-off)]
		w[i] = seq
	}
	c.seq += n
	c.winSz += l
	c.register.Unlock()
	c.snd.L.Unlock()

	// now wait for the acks
	c.ack.L.Lock()
	if func() bool {
		for _, s := range w {
			if _, unsent := s.packets[s]; unsent {
				return false
			}
			return true
		}
	}() {
		c.ack.Wait()
	}
	c.ack.L.Unlock()

	return nil
}
*/

func (c *Conn) run() {
	go func() {
		// loop until Close(), processing the packet queue
		o := &header{}
		for c != nil {
		}
	}()
	go func() {
		i := &header{}
		buf := make([]byte, 1500)
		for c != nil {

		}
	}()
}

func (c *Conn) Read(b []byte) (int, error) {
}

func (c *Conn) Write(b []byte) (int, error) {
}

func (c *Conn) Close() error {
}

func (c *Conn) LocalAddr() Addr {}

func (c *Conn) RemoteAddr() Addr {}

func (c *Conn) SetDeadline(t time.Time) error {}

func (c *Conn) SetReadDeadline(t time.Time) error {}

func (c *Conn) SetWriteDeadline(t time.Time) error {}
