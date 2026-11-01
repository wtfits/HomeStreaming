package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/kardianos/service"

	"github.com/wtfits/homestreaming/internal/app"
	"github.com/wtfits/homestreaming/internal/config"
)

type program struct {
	flags  config.Flags
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(s service.Service) error {
	cfg, err := config.Load(p.flags, nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	opts := app.Options{Version: version, Activate: updateActivator(cfg.DataDir, true)}
	a, err := app.New(ctx, cfg, opts)
	if err != nil {
		cancel()
		return err
	}
	go func() {
		defer close(p.done)
		if err := a.Run(ctx, opts); err != nil {
			a.Log.Error("server stopped", "err", err)
		}
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
	return nil
}

func newService(f config.Flags, user, password string) (service.Service, error) {
	args := []string{"serve"}
	if f.DataDir != "" {
		args = append(args, "--data-dir", f.DataDir)
	}
	cfg := &service.Config{
		Name:        "HomeStreaming",
		DisplayName: "HomeStreaming Media Server",
		Description: "Streams your media library to browsers, iPhone and LG TV (http://localhost:8420).",
		Arguments:   args,
		UserName:    user,
		Option:      service.KeyValue{"Restart": "on-failure", "OnFailure": "restart", "StartType": "automatic"},
	}
	if password != "" {
		cfg.Option["Password"] = password
	}
	if runtime.GOOS == "linux" {
		cfg.Dependencies = []string{"After=network-online.target", "Wants=network-online.target"}
	}
	return service.New(&program{flags: f}, cfg)
}

func runAsService(f config.Flags) error {
	s, err := newService(f, "", "")
	if err != nil {
		return err
	}
	return s.Run()
}

func serviceCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: homestreaming service install|uninstall|start|stop|status [--data-dir DIR] [--user USER --password PASS]")
	}
	action := args[0]
	var user, password string
	rest := []string{}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--user":
			if i+1 < len(args) {
				user = args[i+1]
				i++
			}
		case "--password":
			if i+1 < len(args) {
				password = args[i+1]
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}
	f, _, err := parseFlags(rest)
	if err != nil {
		return err
	}
	s, err := newService(f, user, password)
	if err != nil {
		return err
	}
	switch action {
	case "install":
		if err := s.Install(); err != nil {
			return fmt.Errorf("install service (run as Administrator / root?): %w", err)
		}
		fmt.Println("Service installed. Start it with: homestreaming service start")
		addFirewallRule()
		if cfg, err := config.Load(f, nil); err == nil {
			// Let console runs (as a normal user) share the service's data.
			if err := config.GrantUsersModify(cfg.DataDir, true); err == nil && runtime.GOOS == "windows" {
				fmt.Println("Data folder permissions updated:", cfg.DataDir)
			}
		}
	case "uninstall":
		_ = s.Stop()
		if err := s.Uninstall(); err != nil {
			return err
		}
		fmt.Println("Service removed.")
	case "start":
		if err := s.Start(); err != nil {
			return err
		}
		fmt.Println("Service started. Open http://localhost:8420")
	case "stop":
		if err := s.Stop(); err != nil {
			return err
		}
		fmt.Println("Service stopped.")
	case "status":
		st, err := s.Status()
		if err != nil {
			return err
		}
		names := map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped", service.StatusUnknown: "unknown"}
		fmt.Println("HomeStreaming service:", names[st])
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	return nil
}

const firewallCmd = `netsh advfirewall firewall add rule name="HomeStreaming" dir=in action=allow protocol=TCP localport=8420 profile=any`

func addFirewallRule() {
	if runtime.GOOS != "windows" {
		return
	}
	_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=HomeStreaming").Run()
	out, err := exec.Command("netsh", "advfirewall", "firewall", "add", "rule", "name=HomeStreaming", "dir=in", "action=allow",
		"protocol=TCP", "localport=8420", "profile=any").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not add the firewall rule (%v: %s). Run this in an Administrator prompt:\n  %s\n", err, out, firewallCmd)
		return
	}
	fmt.Println("Windows Firewall rule \"HomeStreaming\" added (TCP 8420, all profiles).")
}
