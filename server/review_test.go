package server

import "testing"

func TestMalformedAddresses(t *testing.T) {
	for _, kind := range []byte{0, 2, 255} {
		for n := 0; n < 19; n++ {
			b := make([]byte, n)
			if n > 2 {
				b[2] = kind
			}
			addr := parseAddress(b)
			valid := (kind == 0 && n >= 7) || (kind == 2 && n >= 19)
			if (addr != nil) != valid {
				t.Fatalf("kind=%d length=%d", kind, n)
			}
		}
	}
}

func TestFullQueueReturnsError(t *testing.T) {
	q := newReqq(1, nil)
	if _, err := q.allocForConn(nil); err != nil {
		t.Fatal(err)
	}
	if req, err := q.allocForConn(nil); req != nil || err == nil {
		t.Fatal("full queue must return an error")
	}
}
