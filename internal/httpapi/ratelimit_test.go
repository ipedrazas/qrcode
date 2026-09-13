package httpapi

import (
	"net/netip"
	"testing"
	"time"
)

func TestClientKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		remote string
		want   string
	}{
		{"192.0.2.1:1234", "192.0.2.1/32"},
		{"[::ffff:192.0.2.1]:80", "192.0.2.1/32"},
		{"[2001:db8:1:2:3:4:5:6]:443", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff:ffff:ffff:ffff]:443", "2001:db8:1:2::/64"},
		{"[fe80::1%en0]:80", "fe80::/64"},
		{"garbage", "invalid Prefix"},
		{"", "invalid Prefix"},
	}
	for _, tc := range cases {
		if got := clientKey(tc.remote).String(); got != tc.want {
			t.Errorf("clientKey(%q) = %s, want %s", tc.remote, got, tc.want)
		}
	}
}

func TestRateLimiterBurstAndRefill(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(2, 3)
	now := time.Unix(0, 0)
	rl.now = func() time.Time { return now }
	key := netip.MustParsePrefix("192.0.2.1/32")

	for i := range 3 {
		if ok, _ := rl.reserve(key); !ok {
			t.Fatalf("request %d within burst denied", i)
		}
	}
	ok, wait := rl.reserve(key)
	if ok || wait != 500*time.Millisecond {
		t.Fatalf("over burst: ok=%v wait=%v, want false 500ms", ok, wait)
	}
	// A denied request must not consume a token.
	now = now.Add(500 * time.Millisecond)
	if ok, _ := rl.reserve(key); !ok {
		t.Fatal("denied after refill interval")
	}
	if ok, _ := rl.reserve(key); ok {
		t.Fatal("allowed with an empty bucket")
	}
}

func TestRateLimiterForgetsIdleClients(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(10, 20)
	now := time.Unix(0, 0)
	rl.now = func() time.Time { return now }

	for i := range 100 {
		rl.reserve(netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), 32))
	}
	if n := len(rl.clients); n != 100 {
		t.Fatalf("tracking %d clients, want 100", n)
	}
	now = now.Add(rl.idleTTL)
	rl.reserve(netip.MustParsePrefix("192.0.2.1/32"))
	if n := len(rl.clients); n != 1 {
		t.Fatalf("after idle TTL tracking %d clients, want 1", n)
	}
}
