// Package auth implements password hashing, opaque session tokens, login
// lockout and per-IP rate limiting.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"
	"golang.org/x/time/rate"

	"github.com/wtfits/homestreaming/internal/db"
)

// Params are the Argon2id parameters (DESIGN §5).
var Params = &argon2id.Params{Memory: 64 * 1024, Iterations: 2, Parallelism: 2, SaltLength: 16, KeyLength: 32}

const (
	SessionLifetime = 90 * 24 * time.Hour
	CookieName      = "hs_session"
	MinPasswordLen  = 8
	lockThreshold   = 5
	lockBase        = 60 * time.Second
	lockMax         = time.Hour
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUnauthenticated    = errors.New("unauthenticated")
)

// LockedError is returned while an account is locked out.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string {
	return fmt.Sprintf("too many failed attempts; try again in %d seconds", int(e.RetryAfter.Seconds()+0.999))
}

// HashPassword returns an Argon2id PHC string.
func HashPassword(pw string) (string, error) { return argon2id.CreateHash(pw, Params) }

// VerifyPassword checks pw against an Argon2id hash.
func VerifyPassword(pw, hash string) bool {
	ok, err := argon2id.ComparePasswordAndHash(pw, hash)
	return err == nil && ok
}

// NewToken returns a random base64url token (32 bytes).
func NewToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// HashToken returns the hex sha256 of a token (what the DB stores).
func HashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// LockDuration returns the lock length after n consecutive failures (0 if not locked).
func LockDuration(failures int) time.Duration {
	if failures < lockThreshold {
		return 0
	}
	d := lockBase
	for i := lockThreshold; i < failures; i++ {
		d *= 2
		if d >= lockMax {
			return lockMax
		}
	}
	return d
}

// Service implements login and session validation.
type Service struct {
	DB  *db.DB
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// DeviceInfo describes the logging-in client.
type DeviceInfo struct {
	DeviceID   string
	DeviceName string
	ClientType string
	UserAgent  string
	IP         string
}

// Login verifies credentials (with lockout) and creates a session.
func (s *Service) Login(ctx context.Context, username, password string, dev DeviceInfo) (string, *db.Session, *db.User, error) {
	key := "u:" + strings.ToLower(strings.TrimSpace(username))
	att, err := s.DB.GetLoginAttempt(ctx, key)
	if err != nil {
		return "", nil, nil, err
	}
	now := s.now()
	if att.LockedUntil.After(now) {
		return "", nil, nil, &LockedError{RetryAfter: att.LockedUntil.Sub(now)}
	}
	user, err := s.DB.GetUserByUsername(ctx, strings.TrimSpace(username))
	ok := err == nil && !user.Disabled && VerifyPassword(password, user.PasswordHash)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return "", nil, nil, err
	}
	if !ok {
		if user == nil {
			// Spend comparable time to avoid username probing.
			_, _ = HashPassword(password)
		}
		att.Failures++
		if d := LockDuration(att.Failures); d > 0 {
			att.LockedUntil = now.Add(d)
			_ = s.DB.SetLoginAttempt(ctx, att)
			return "", nil, nil, &LockedError{RetryAfter: d}
		}
		att.LockedUntil = time.Time{}
		_ = s.DB.SetLoginAttempt(ctx, att)
		return "", nil, nil, ErrInvalidCredentials
	}
	_ = s.DB.ResetLoginAttempt(ctx, key)
	tok, sess, err := s.CreateSession(ctx, user, dev)
	return tok, sess, user, err
}

// CreateSession creates a new session for user.
func (s *Service) CreateSession(ctx context.Context, user *db.User, dev DeviceInfo) (string, *db.Session, error) {
	tok := NewToken()
	now := s.now()
	if dev.DeviceID == "" {
		dev.DeviceID = db.NewID()
	}
	if dev.DeviceName == "" {
		dev.DeviceName = "Unknown device"
	}
	if dev.ClientType == "" {
		dev.ClientType = "web"
	}
	sess := &db.Session{
		ID: db.NewID(), UserID: user.ID, TokenHash: HashToken(tok),
		DeviceID: dev.DeviceID, DeviceName: truncate(dev.DeviceName, 100), ClientType: dev.ClientType,
		UserAgent: truncate(dev.UserAgent, 300), IP: dev.IP,
		CreatedAt: db.FormatTime(now), LastSeenAt: db.FormatTime(now), ExpiresAt: db.FormatTime(now.Add(SessionLifetime)),
	}
	if err := s.DB.CreateSession(ctx, sess); err != nil {
		return "", nil, err
	}
	return tok, sess, nil
}

// Authenticate resolves a token to its user and session, extending the
// session (sliding expiry) when it was last seen more than a day ago.
func (s *Service) Authenticate(ctx context.Context, token, ip string) (*db.User, *db.Session, error) {
	if token == "" {
		return nil, nil, ErrUnauthenticated
	}
	sess, err := s.DB.GetSessionByTokenHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, nil, ErrUnauthenticated
		}
		return nil, nil, err
	}
	now := s.now()
	if db.ParseTime(sess.ExpiresAt).Before(now) {
		_ = s.DB.DeleteSession(ctx, sess.ID)
		return nil, nil, ErrUnauthenticated
	}
	user, err := s.DB.GetUser(ctx, sess.UserID)
	if err != nil || user.Disabled {
		return nil, nil, ErrUnauthenticated
	}
	if now.Sub(db.ParseTime(sess.LastSeenAt)) > 24*time.Hour || (ip != "" && ip != sess.IP) {
		exp := now.Add(SessionLifetime)
		_ = s.DB.TouchSession(ctx, sess.ID, ip, now, exp)
		sess.LastSeenAt, sess.ExpiresAt, sess.IP = db.FormatTime(now), db.FormatTime(exp), ip
	}
	return user, sess, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- request identity ---

type ctxKey struct{}

// Identity is the authenticated caller.
type Identity struct {
	User      *db.User
	Session   *db.Session
	Token     string
	ViaCookie bool
}

// WithIdentity stores id in ctx.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the caller identity (nil when unauthenticated).
func FromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(ctxKey{}).(*Identity)
	return id
}

// TokenFromRequest extracts the token: Authorization header, then cookie, then
// (only when allowQuery) the ?token= parameter.
func TokenFromRequest(r *http.Request, allowQuery bool) (tok string, viaCookie bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[7:]), false
		}
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	if allowQuery {
		if t := r.URL.Query().Get("token"); t != "" {
			return t, false
		}
	}
	return "", false
}

// --- rate limiting ---

// IPLimiter is a per-IP token bucket limiter.
type IPLimiter struct {
	mu      sync.Mutex
	r       rate.Limit
	burst   int
	buckets map[string]*limEntry
}

type limEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewIPLimiter allows `perMinute` events per minute with the given burst.
func NewIPLimiter(perMinute float64, burst int) *IPLimiter {
	return &IPLimiter{r: rate.Limit(perMinute / 60), burst: burst, buckets: map[string]*limEntry{}}
}

// Allow consumes a token for ip; when denied it returns the wait time.
func (l *IPLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e := l.buckets[ip]
	if e == nil {
		if len(l.buckets) > 10000 {
			for k, v := range l.buckets {
				if now.Sub(v.seen) > 10*time.Minute {
					delete(l.buckets, k)
				}
			}
		}
		e = &limEntry{lim: rate.NewLimiter(l.r, l.burst)}
		l.buckets[ip] = e
	}
	e.seen = now
	res := e.lim.ReserveN(now, 1)
	if !res.OK() {
		return false, time.Minute
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, d
	}
	return true, 0
}
