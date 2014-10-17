package utp

func minU16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func minU32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func wrapCmp(l, r, mask uint32) bool {
	return ((r - l) & mask) < ((l - r) & mask)
}
