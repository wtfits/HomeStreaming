package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"

	"github.com/wtfits/homestreaming/internal/config"
	"github.com/wtfits/homestreaming/internal/update"
)

// updateCmd implements
//
//	homestreaming update [--check] [--data-dir DIR]   install the latest release
//	homestreaming update finish --data-dir DIR        (internal) restart helper
func updateCmd(args []string) error {
	if len(args) > 0 && args[0] == "finish" {
		return updateFinish(args[1:])
	}
	checkOnly := false
	var rest []string
	for _, a := range args {
		if a == "--check" || a == "-check" {
			checkOnly = true
		} else {
			rest = append(rest, a)
		}
	}
	f, _, err := parseFlags(rest)
	if err != nil {
		return err
	}
	cfg, err := config.Load(f, nil)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	u := &update.Updater{Repo: cfg.Update.Repo, Current: version, DataDir: cfg.DataDir}
	fmt.Printf("HomeStreaming %s: checking github.com/%s for updates...\n", version, cfg.Update.Repo)
	if err := u.Check(ctx); err != nil {
		return err
	}
	st := u.Status()
	if st.Latest == nil {
		fmt.Println("No releases have been published yet.")
		return nil
	}
	fmt.Printf("Latest release: %s (published %s)\n", st.Latest.Version, st.Latest.PublishedAt.Local().Format("2006-01-02 15:04"))
	if !st.Available {
		fmt.Println("You are up to date.")
		return nil
	}
	if checkOnly {
		fmt.Println("An update is available. Install it with: homestreaming update")
		return nil
	}
	if !st.CanInstall {
		return errors.New(st.CannotInstallReason)
	}
	fmt.Println("Downloading and verifying...")
	p, err := u.Install(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s. The previous version is kept as %s.\n", p.To, p.Old)

	if svc, err := newService(f, "", ""); err == nil {
		if s, err := svc.Status(); err == nil && s == service.StatusRunning {
			fmt.Println("Restarting the HomeStreaming service...")
			r := update.FinishService(ctx, cfg.DataDir, svcControl{svc}, *p, update.FinishOptions{
				HealthURL: update.HealthURL(cfg.HTTP.Addr),
				Logf:      func(format string, a ...any) { fmt.Printf("  "+format+"\n", a...) },
			})
			if !r.OK {
				return errors.New(r.Message)
			}
			fmt.Println(r.Message)
			return nil
		}
	}
	fmt.Println("Start HomeStreaming again to run the new version.")
	return nil
}

// updateActivator returns how a server restarts into a freshly installed
// binary. The Windows service starts a detached helper ("update finish") that
// restarts the service, checks the new version answers and rolls back if not.
// Elsewhere the user restarts HomeStreaming (systemd: systemctl restart).
func updateActivator(dataDir string, asService bool) update.Activator {
	if runtime.GOOS != "windows" || !asService {
		return nil
	}
	return func(p update.Pending) error {
		cmd := exec.Command(p.Exe, "update", "finish", "--data-dir", dataDir)
		cmd.Dir = filepath.Dir(p.Exe)
		cmd.SysProcAttr = detachedProcAttr()
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start the restart helper: %w", err)
		}
		return cmd.Process.Release()
	}
}

// updateFinish is the helper process started by updateActivator.
func updateFinish(args []string) error {
	f, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(f, nil)
	if err != nil {
		return err
	}
	logPath := filepath.Join(cfg.DataDir, "logs", "update.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	logf := func(format string, a ...any) {
		line := time.Now().Format(time.RFC3339) + " " + fmt.Sprintf(format, a...) + "\n"
		if lf != nil {
			_, _ = lf.WriteString(line)
		}
		fmt.Print(line)
	}
	if lf != nil {
		defer lf.Close()
	}
	p, err := update.ReadPending(cfg.DataDir)
	if err != nil {
		logf("no pending update: %v", err)
		return err
	}
	// Give the server a moment to answer the admin's install request.
	time.Sleep(2 * time.Second)
	svc, err := newService(f, "", "")
	if err != nil {
		logf("service control: %v", err)
		return err
	}
	r := update.FinishService(context.Background(), cfg.DataDir, svcControl{svc}, *p, update.FinishOptions{
		HealthURL: update.HealthURL(cfg.HTTP.Addr),
		Logf:      logf,
	})
	if !r.OK {
		return errors.New(r.Message)
	}
	return nil
}

// svcControl adapts kardianos/service to update.ServiceControl.
type svcControl struct{ s service.Service }

func (c svcControl) Stop() error  { return c.s.Stop() }
func (c svcControl) Start() error { return c.s.Start() }
func (c svcControl) Running() (bool, error) {
	st, err := c.s.Status()
	return st == service.StatusRunning, err
}
