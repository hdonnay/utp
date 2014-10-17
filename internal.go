package utp

import "time"

const (
	curDelaySize     = 3
	delayBaseSamples = 13

	timestampMask = 0xFFFFFFFF
)

// A lot of this is transliterated from the C++ libutp and could probably stand
// to be a bit more idiomatic.
type hist struct {
	curDelayHist []uint32
	curDelayIdx  int

	delayBaseHist []uint32
	delayBaseIdx  int
	delayBase     uint32
	delayBaseTime time.Time
}

func (h *hist) Initialize(sample uint32) {
	h.curDelay = make([]uint32, curDelaySize)
	h.delayBaseHist = make([]uint32, delayBaseSamples)
	h.delayBase = sample
	h.delayBaseTime = time.Now()
	for i := range h.delayBaseHist {
		h.delayBaseHist[i] = sample
	}
}

func (h *hist) Shift(off uint32) {
	for i := range baseDelayHist {
		baseDelayHist[i] += shift
	}
	baseDelay += off
}

func (h *hist) Add(sample uint32) {
	cur := time.Now()
	if wrapCmp(sample, h.delayBaseHist[h.delayBaseIdx], timestampMask) {
		h.delayBaseHist[delayBaseIdx] = sample
	}
	if wrapCmp(sample, h.delayBase, timestampMask) {
		h.delayBase = sample
	}

	d := sample - h.delayBase
	h.curDelayHist[h.curDelayIdx] = d
	h.curDelayIdx = (h.curDelayIdx + 1) % curDelaySize

	// once a minute, re-evaluate the delayBase
	if cur.After(h.delayBaseTime.Add(1 * time.Minute)) {
		h.delayBaseTime = cur
		h.delayBaseIdx = (h.delayBaseIdx + 1) % delayBaseSamples
		h.delayBaseHist[h.delayBaseIdx] = sample
		h.delayBase = h.delayBaseHist[0]
		for _, v := range h.delayBaseHist {
			if wrapCmp(v, h.delayBase, timestampMask) {
				h.delayBase = v
			}
		}
	}
}

func (h *hist) Get() uint32 {
	v := uint32(-1)
	for _, x := range h.curDelayHist {
		v = minU32(v, x)
	}
	return v
}

type packetBuf struct {
	mask uint
	// yay pointers >_>
	pkt *[]*packet
}

func (p *packetBuf) Get(idx uint) *packet {
	return p.pkt[idx&mask]
}

func (p *packetBuf) Put(idx uint, d *packet) {
	p.pkt[idx&mask] = d
}

func (p *packetBuf) Grow(item, idx uint) {
	sz := mask + 1
	sz *= 2
	for idx < sz {
		sz *= 2
	}
	n := make([][]byte, sz)
	sz--
	for i := 0; i <= p.mask; i++ {
		n[(item-idx+i)&sz] = p.get(item - idx + i)
	}
	p.mask = sz
	p.pkt = &n
}

func (p *packetBuf) Ensure(item, idx, uint) {
	if idx > p.mask {
		p.Grow(item, idx)
	}
}

func (p *packetBuf) Size() uint { return p + 1 }

type packet struct {
	Tx         int
	Header     *header
	Payload    []byte
	Sent       time.Time
	NeedResend bool
}

func (p *packet) Len() int {
	return p.Header.Len() + len(p.Payload)
}
