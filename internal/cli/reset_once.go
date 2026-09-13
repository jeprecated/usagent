package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jeprecated/usagent/internal/clientpolicy"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

func RunResetOnce(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "arm" && args[0] != "status" && args[0] != "cancel") {
		return errors.New("usage: usagent reset-once arm|status|cancel [options]")
	}
	action := args[0]
	fs := flag.NewFlagSet("usagent reset-once", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := UsageOptions{ConfigPath: config.DefaultConfigPath(), Timeout: 10 * time.Second}
	var acknowledge bool
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "YAML config file")
	fs.StringVar(&opts.DaemonURL, "daemon-url", "", "local daemon origin")
	fs.StringVar(&opts.Host, "host", "", "local daemon host")
	fs.IntVar(&opts.Port, "port", 0, "daemon port")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "request timeout")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON")
	fs.BoolVar(&acknowledge, "acknowledge-unknown", false, "acknowledge an outcome already manually reconciled")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || (acknowledge && action != "cancel") {
		return errors.New("unexpected reset-once arguments; --acknowledge-unknown is only valid for cancel")
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "daemon-url":
			opts.DaemonURLSet = true
		case "host":
			opts.HostSet = true
		case "port":
			opts.PortSet = true
		}
	})
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	resolution, err := clientpolicy.Resolve(cfg, opts.policyFlags())
	if err != nil {
		return err
	}
	if resolution.Local {
		return errors.New("reset-once requires the running daemon; local fallback is forbidden")
	}
	u, err := url.Parse(resolution.Origin)
	if err != nil {
		return err
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("reset-once only controls a local loopback daemon; use --daemon-url http://127.0.0.1:PORT")
	}
	u.Path = "/v1/chatgpt/reset-once"
	method := http.MethodGet
	var body io.Reader
	confirm := action + "-chatgpt-reset-once"
	if action != "status" {
		u.Path += "/" + action
		method = http.MethodPost
		b, _ := json.Marshal(struct {
			Confirm            string `json:"confirm"`
			AcknowledgeUnknown bool   `json:"acknowledgeUnknown,omitempty"`
		}{confirm, acknowledge})
		body = bytes.NewReader(b)
	}
	if opts.Timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Usagent-Action", confirm)
	}
	// Never route spending authority through an environment-configured proxy,
	// redirect, alternate daemon or local fallback after an ambiguous response.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reset-once request failed; check status before retrying: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body)
		return fmt.Errorf("reset-once HTTP %d: %s; do not retry blindly; check status", resp.StatusCode, body.Error.Message)
	}
	var state model.ChatGPTResetOnce
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&state); err != nil {
		return fmt.Errorf("invalid reset-once response; check status before retrying: %w", err)
	}
	if state.Version != 1 || state.Status == "" {
		return errors.New("invalid reset-once response; check status before retrying")
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(state)
	}
	_, err = fmt.Fprintln(stdout, formatResetOnce(state))
	return err
}

func formatResetOnce(s model.ChatGPTResetOnce) string {
	line := "ChatGPT reset-once: " + s.Status
	if s.Message != "" {
		line += " — " + strings.Join(strings.Fields(s.Message), " ")
	}
	return line
}
