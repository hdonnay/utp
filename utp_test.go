package utp

import "testing"

func TestHeader(t *testing.T) {
	b := make([]byte, 20)
	h := &header{
		VerType: 1<<4 | Syn,
		Ex:      false,
	}
	t.Logf("%x\n", b)
	if err := h.Ser(b); err != nil {
		t.Fatal(err)
	}
	t.Logf("%x\n", b)
	if err := h.Deser(b); err != nil {
		t.Fatal(err)
	}
	if h.Type() != Syn {
		t.Fatal("incorrect type")
	}

	b[0] |= 1 << 5
	t.Logf("%x\n", b)
	if err := h.Deser(b); err == nil {
		t.Fatal("no error on corrupted version")
	}
}
