package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

// cmdLogin signs this machine's tend (TUI and CLI) in to a tend-server through a browser: the device code flow of
// RFC 8628. It talks only net/http, never internal/server, since a mode 1 build of cmd/tend carries no server code.
func cmdLogin(args []string) error {
	fs := newFlags("login")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := loadConfig()
	url := fs.Arg(0)
	if url == "" && cfg.Coordinator != nil {
		url = cfg.Coordinator.URL
	}
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if url == "" {
		return i18n.E("cli.login.need_url")
	}
	name, _ := os.Hostname()
	client := &http.Client{Timeout: 15 * time.Second}

	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		VerifyURL  string `json:"verify_url"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := postJSON(client, url+"/auth/device", map[string]string{"name": name}, &start); err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.login.verify", start.VerifyURL, start.UserCode))

	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 3 * time.Second
	}
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return i18n.E("cli.login.canceled")
		case <-ticker.C:
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return i18n.E("cli.login.expired")
		}
		var poll struct {
			Status string `json:"status"`
			Token  string `json:"token"`
			User   string `json:"user"`
		}
		if err := postJSON(client, url+"/auth/device/token", map[string]string{"device_code": start.DeviceCode}, &poll); err != nil {
			return err
		}
		switch poll.Status {
		case "pending":
			continue
		case "denied":
			return i18n.E("cli.login.denied")
		case "expired":
			return i18n.E("cli.login.expired")
		case "ok":
			return saveLogin(url, poll.Token, poll.User)
		default:
			return i18n.E("cli.login.bad_status", poll.Status)
		}
	}
}

// saveLogin writes the new token beside tend's other state and points config.coordinator at this server.
func saveLogin(url, token, user string) error {
	path := filepath.Join(tend.Home(), "coordinator.token")
	if err := fileio.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	cfg := loadConfig()
	cfg.Coordinator = &tend.CoordinatorConfig{URL: url, TokenFile: path}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.login.done", user, path))
	return nil
}

func postJSON(c *http.Client, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return i18n.E("cli.login.unreachable", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return i18n.E("cli.login.http_error", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
