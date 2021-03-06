package utp

import (
	"sync"
	"time"
)

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

func (h *hist) Initialize() {
	h.curDelayHist = make([]uint32, curDelaySize)
	h.delayBaseHist = make([]uint32, delayBaseSamples)
	h.delayBaseTime = time.Now()
	h.delayBase = ticksSince(h.delayBaseTime)
	for i := range h.delayBaseHist {
		h.delayBaseHist[i] = h.delayBase
	}
}

func (h *hist) Shift(off uint32) {
	for i := range h.delayBaseHist {
		h.delayBaseHist[i] += off
	}
	h.delayBase += off
}

func (h *hist) Add(sample uint32) {
	cur := time.Now()
	if wrapCmp(sample, h.delayBaseHist[h.delayBaseIdx], timestampMask) {
		h.delayBaseHist[h.delayBaseIdx] = sample
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
	v := ^uint32(0)
	for _, x := range h.curDelayHist {
		v = minU32(v, x)
	}
	return v
}

type packet struct {
	*sync.RWMutex
	Tx         int
	Header     *header
	Payload    []byte
	Sent       time.Time
	NeedResend bool
}

func (p *packet) Len() int {
	return p.Header.Len() + len(p.Payload)
}

// Bytes serializes the header and payload and returns a byte slice of it
func (p *packet) Bytes() []byte {
	return nil
}
