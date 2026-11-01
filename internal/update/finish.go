package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ServiceControl stops and starts the installed service (kardianos/service
// in production, a fake in tests).
type ServiceControl interface {
	Stop() error
	Start() error
	Running() (bool, error)
}

// FinishOptions tune FinishService.
type FinishOptions struct {
	HealthURL    string        // GET → JSON with "version"; see HealthURL
	StartTimeout time.Duration // how long the new version gets to answer; default 90 s
	StopTimeout  time.Duration // default 60 s
	Poll         time.Duration // default 1 s
	Logf         func(format string, args ...any)
	Client       *http.Client
}

// FinishService restarts the service so the new executable runs, waits until
// the server reports version p.To and otherwise restores the previous
// executable and restarts again. It records the outcome in result.json.
func FinishService(ctx context.Context, dataDir string, svc ServiceControl, p Pending, o FinishOptions) Result {
	if o.StartTimeout == 0 {
		o.StartTimeout = 90 * time.Second
	}
	if o.StopTimeout == 0 {
		o.StopTimeout = 60 * time.Second
	}
	if o.Poll == 0 {
		o.Poll = time.Second
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 5 * time.Second}
	}

	o.Logf("restarting the service to run %s (was %s)", p.To, p.From)
	err := restart(ctx, svc, o)
	if err == nil {
		if err = waitVersion(ctx, o, p.To); err == nil {
			r := Result{From: p.From, To: p.To, OK: true, Message: "Updated from " + p.From + " to " + p.To + ".", At: time.Now().UTC()}
			ClearPending(dataDir)
			_ = WriteResult(dataDir, r)
			o.Logf("update to %s is running", p.To)
			return r
		}
	}
	o.Logf("update to %s failed: %v; rolling back to %s", p.To, err, p.From)
	_ = stopWait(ctx, svc, o)
	msg := fmt.Sprintf("The update to %s failed (%v), so %s was restored.", p.To, err, p.From)
	if rerr := Rollback(p.Exe, p.Old); rerr != nil {
		msg = fmt.Sprintf("The update to %s failed (%v) and restoring %s also failed: %v", p.To, err, p.From, rerr)
	}
	ClearPending(dataDir)
	r := Result{From: p.From, To: p.To, OK: false, Message: msg, At: time.Now().UTC()}
	_ = WriteResult(dataDir, r)
	if serr := svc.Start(); serr != nil {
		o.Logf("starting the service after rollback failed: %v", serr)
	} else if werr := waitVersion(ctx, o, p.From); werr != nil {
		o.Logf("previous version did not answer after rollback: %v", werr)
	}
	o.Logf("%s", msg)
	return r
}

func restart(ctx context.Context, svc ServiceControl, o FinishOptions) error {
	if err := stopWait(ctx, svc, o); err != nil {
		return err
	}
	// The service manager may still be finishing the stop; retry briefly.
	var err error
	for i := 0; i < 10; i++ {
		if err = svc.Start(); err == nil {
			return nil
		}
		if serr := sleep(ctx, o.Poll); serr != nil {
			return serr
		}
	}
	return fmt.Errorf("start service: %w", err)
}

func stopWait(ctx context.Context, svc ServiceControl, o FinishOptions) error {
	if running, err := svc.Running(); err == nil && !running {
		return nil
	}
	if err := svc.Stop(); err != nil {
		// Stop can fail when the service already stopped; trust the status.
		if running, serr := svc.Running(); serr != nil || running {
			return fmt.Errorf("stop service: %w", err)
		}
	}
	deadline := time.Now().Add(o.StopTimeout)
	for time.Now().Before(deadline) {
		if running, err := svc.Running(); err == nil && !running {
			return nil
		}
		if err := sleep(ctx, o.Poll); err != nil {
			return err
		}
	}
	return errors.New("service did not stop in time")
}

func waitVersion(ctx context.Context, o FinishOptions, want string) error {
	deadline := time.Now().Add(o.StartTimeout)
	last := "no answer"
	for time.Now().Before(deadline) {
		v, err := fetchVersion(ctx, o.Client, o.HealthURL)
		switch {
		case err != nil:
			last = err.Error()
		case SameVersion(v, want):
			return nil
		default:
			last = "server reports version " + v
		}
		if err := sleep(ctx, o.Poll); err != nil {
			return err
		}
	}
	return fmt.Errorf("version %s did not come up within %s (%s)", want, o.StartTimeout, last)
}

func fetchVersion(ctx context.Context, c *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return "", err
	}
	return body.Version, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// HealthURL turns a listen address (":8420", "0.0.0.0:8420", "192.168.0.5:8420")
// into the local /system/info URL used to confirm which version is running.
func HealthURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", strings.TrimPrefix(addr, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/v1/system/info"
}
