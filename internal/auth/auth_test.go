package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wtfits/homestreaming/internal/db"
)

func TestPasswordAndToken(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse", h) || VerifyPassword("wrong", h) {
		t.Fatal("verify mismatch")
	}
	a, b := NewToken(), NewToken()
	if a == b || len(a) < 40 || HashToken(a) == HashToken(b) || len(HashToken(a)) != 64 {
		t.Fatal("token generation")
	}
}

func TestLockDuration(t *testing.T) {
	cases := map[int]time.Duration{0: 0, 4: 0, 5: time.Minute, 6: 2 * time.Minute, 7: 4 * time.Minute, 11: 64 * time.Minute, 20: time.Hour}
	for n, want := range cases {
		got := LockDuration(n)
		if want == 64*time.Minute {
			want = time.Hour
		}
		if got != want {
			t.Errorf("failures %d: %v want %v", n, got, want)
		}
	}
}

func TestLoginLockout(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	h, _ := HashPassword("password123")
	if _, err := d.CreateUser(ctx, "can", h, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s := &Service{DB: d, Now: func() time.Time { return now }}
	for i := 1; i <= 4; i++ {
		_, _, _, err := s.Login(ctx, "can", "nope", DeviceInfo{})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, _, _, err = s.Login(ctx, "can", "nope", DeviceInfo{})
	var le *LockedError
	if !errors.As(err, &le) || le.RetryAfter != time.Minute {
		t.Fatalf("5th failure should lock 60s: %v", err)
	}
	// Even the right password is refused while locked.
	if _, _, _, err := s.Login(ctx, "can", "password123", DeviceInfo{}); !errors.As(err, &le) {
		t.Fatalf("expected locked: %v", err)
	}
	now = now.Add(61 * time.Second)
	tok, sess, user, err := s.Login(ctx, "CAN", "password123", DeviceInfo{DeviceID: "dev1", DeviceName: "Test"})
	if err != nil || tok == "" || sess == nil || user.Username != "can" {
		t.Fatalf("login after lock: %v", err)
	}
	u2, s2, err := s.Authenticate(ctx, tok, "1.2.3.4")
	if err != nil || u2.ID != user.ID || s2.ID != sess.ID {
		t.Fatalf("authenticate: %v", err)
	}
	// Same device id replaces the previous session.
	tok2, _, _, err := s.Login(ctx, "can", "password123", DeviceInfo{DeviceID: "dev1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Authenticate(ctx, tok, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("old device session should be replaced")
	}
	if _, _, err := s.Authenticate(ctx, tok2, ""); err != nil {
		t.Fatal(err)
	}
	// Expiry.
	now = now.Add(SessionLifetime + time.Hour)
	if _, _, err := s.Authenticate(ctx, tok2, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("expired session accepted")
	}
}

func TestIPLimiter(t *testing.T) {
	l := NewIPLimiter(10, 10)
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow("1.1.1.1"); !ok {
			t.Fatalf("request %d denied", i+1)
		}
	}
	ok, wait := l.Allow("1.1.1.1")
	if ok || wait <= 0 {
		t.Fatal("11th request should be limited")
	}
	if ok, _ := l.Allow("2.2.2.2"); !ok {
		t.Fatal("other ip limited")
	}
}
