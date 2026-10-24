package upstream

import (
	"net"
	"testing"
	"time"
)

func TestTrackCountsBytesAndRate(t *testing.T) {
	u := New("u", "127.0.0.1:1", Params{})
	a, b := net.Pipe()
	defer b.Close()
	c := u.Track(a)

	go func() {
		buf := make([]byte, 64)
		n, _ := b.Read(buf)
		b.Write(buf[:n*2])
	}()
	now := time.Now()
	u.SampleRate(now)
	c.Write(make([]byte, 10))
	got := 0
	buf := make([]byte, 64)
	for got < 20 {
		n, err := c.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got += n
	}
	u.SampleRate(now.Add(2 * time.Second))

	sn := u.Snapshot()
	if sn.TxBytes != 10 || sn.RxBytes != 20 {
		t.Fatalf("bytes tx=%d rx=%d, want 10/20", sn.TxBytes, sn.RxBytes)
	}
	if sn.TxRate != 5 || sn.RxRate != 10 {
		t.Fatalf("rate tx=%d rx=%d, want 5/10", sn.TxRate, sn.RxRate)
	}
	if n := c.(interface{ UpstreamName() string }).UpstreamName(); n != "u" {
		t.Fatalf("name %q", n)
	}
}
