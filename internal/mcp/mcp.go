package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jmalloc/usagent/internal/cli"
	"github.com/jmalloc/usagent/internal/config"
)

type Options struct {
	ConfigPath string
	Host       string
	Port       int
	Timeout    time.Duration
	Offline    bool
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}
	server := &server{opts: opts, in: bufio.NewReader(stdin), out: stdout, stderr: stderr}
	return server.run(ctx)
}

func parseFlags(args []string) (Options, error) {
	fs := flag.NewFlagSet("usagent mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := Options{ConfigPath: config.DefaultConfigPath(), Timeout: 10 * time.Second}
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to YAML config file")
	fs.StringVar(&opts.Host, "host", opts.Host, "daemon listen host override")
	fs.IntVar(&opts.Port, "port", opts.Port, "daemon listen port override")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "daemon/local refresh timeout")
	fs.BoolVar(&opts.Offline, "offline", false, "skip daemon and refresh/read usage locally")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected mcp arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

type server struct {
	opts   Options
	in     *bufio.Reader
	out    io.Writer
	stderr io.Writer
}

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *server) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := readMessage(s.in)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var req rpcRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = writeMessage(s.out, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
			continue
		}
		if req.ID == nil {
			_ = s.handleNotification(req)
			continue
		}
		result, rpcErr := s.handleRequest(ctx, req)
		res := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if rpcErr != nil {
			res.Result = nil
		}
		if err := writeMessage(s.out, res); err != nil {
			return err
		}
	}
}

func (s *server) handleNotification(req rpcRequest) error {
	// MCP lifecycle notifications do not require responses.
	return nil
}

func (s *server) handleRequest(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "usagent", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		res, err := s.callTool(ctx, req.Params)
		if err != nil {
			return toolResult("", err.Error(), true), nil
		}
		return res, nil
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

func tools() []map[string]any {
	return []map[string]any{
		{
			"name":        "usage",
			"description": "Return current provider quota/usage for agent backends. Uses the running usagent daemon when reachable, with the same local fallback behavior as the CLI.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"json":    map[string]any{"type": "boolean", "description": "Return raw schema v2 JSON instead of the human summary."},
					"offline": map[string]any{"type": "boolean", "description": "Skip daemon lookup for this call and refresh/read locally."},
				},
			},
		},
		{
			"name":        "tokens_to_burn",
			"description": "Return likely use-it-or-lose-it quota/tokens that may expire unused. This is backed by usagent expiring-usage and prefers a running daemon when reachable.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"within":                  map[string]any{"type": "string", "description": "Only include opportunities resetting within this Go duration, for example 24h or 90m."},
					"withinMs":                map[string]any{"type": "integer", "description": "Only include opportunities resetting within this many milliseconds."},
					"minimumRemainingPercent": map[string]any{"type": "number", "minimum": 0, "maximum": 100, "description": "Minimum remaining quota percent to include."},
					"providers":               arrayOrStringSchema("Provider IDs to include, for example [\"chatgpt\", \"claude-code\"]."),
					"tiers":                   arrayOrStringSchema("Provider/model tiers to include."),
					"tags":                    arrayOrStringSchema("Provider/model tags to include."),
					"includeLowConfidence":    map[string]any{"type": "boolean", "description": "Include low-confidence opportunities."},
					"json":                    map[string]any{"type": "boolean", "description": "Return raw expiring-usage JSON instead of the human summary."},
					"offline":                 map[string]any{"type": "boolean", "description": "Skip daemon lookup for this call and refresh/read locally."},
				},
			},
		},
	}
}

func arrayOrStringSchema(description string) map[string]any {
	return map[string]any{
		"description": description,
		"oneOf": []any{
			map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			map[string]any{"type": "string", "description": "Comma-separated values."},
		},
	}
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *server) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var call toolCallParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &call); err != nil {
			return nil, fmt.Errorf("invalid tools/call params: %w", err)
		}
	}
	switch call.Name {
	case "usage":
		return s.callUsage(ctx, call.Arguments)
	case "tokens_to_burn", "expiring_usage":
		return s.callTokensToBurn(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

func (s *server) callUsage(ctx context.Context, args map[string]any) (any, error) {
	cliArgs := s.baseCLIArgs(args)
	if boolArg(args, "json") {
		cliArgs = append(cliArgs, "--json")
	}
	var stdout, stderr bytes.Buffer
	if err := cli.RunUsage(ctx, cliArgs, &stdout, &stderr); err != nil {
		return toolResult(stdout.String(), err.Error(), true), nil
	}
	return toolResult(stdout.String(), stderr.String(), false), nil
}

func (s *server) callTokensToBurn(ctx context.Context, args map[string]any) (any, error) {
	cliArgs := s.baseCLIArgs(args)
	if boolArg(args, "json") {
		cliArgs = append(cliArgs, "--json")
	}
	if v := stringArg(args, "within"); v != "" {
		cliArgs = append(cliArgs, "--within", v)
	}
	if v, ok := int64Arg(args, "withinMs"); ok {
		cliArgs = append(cliArgs, "--within-ms", strconv.FormatInt(v, 10))
	}
	if v, ok := floatArg(args, "minimumRemainingPercent"); ok {
		cliArgs = append(cliArgs, "--minimum-remaining-percent", strconv.FormatFloat(v, 'f', -1, 64))
	}
	if v := listArg(args, "providers"); v != "" {
		cliArgs = append(cliArgs, "--providers", v)
	}
	if v := listArg(args, "tiers"); v != "" {
		cliArgs = append(cliArgs, "--tiers", v)
	}
	if v := listArg(args, "tags"); v != "" {
		cliArgs = append(cliArgs, "--tags", v)
	}
	if boolArg(args, "includeLowConfidence") {
		cliArgs = append(cliArgs, "--include-low-confidence")
	}
	var stdout, stderr bytes.Buffer
	if err := cli.RunExpiringUsage(ctx, cliArgs, &stdout, &stderr); err != nil {
		return toolResult(stdout.String(), err.Error(), true), nil
	}
	return toolResult(stdout.String(), stderr.String(), false), nil
}

func (s *server) baseCLIArgs(args map[string]any) []string {
	out := []string{"--config", s.opts.ConfigPath, "--timeout", s.opts.Timeout.String()}
	if s.opts.Host != "" {
		out = append(out, "--host", s.opts.Host)
	}
	if s.opts.Port != 0 {
		out = append(out, "--port", strconv.Itoa(s.opts.Port))
	}
	if s.opts.Offline || boolArg(args, "offline") {
		out = append(out, "--offline")
	}
	return out
}

func toolResult(output, diagnostics string, isError bool) map[string]any {
	content := []map[string]string{}
	if strings.TrimSpace(output) != "" {
		content = append(content, map[string]string{"type": "text", "text": output})
	}
	if strings.TrimSpace(diagnostics) != "" {
		content = append(content, map[string]string{"type": "text", "text": "Diagnostics:\n" + diagnostics})
	}
	if len(content) == 0 {
		content = append(content, map[string]string{"type": "text", "text": ""})
	}
	return map[string]any{"content": content, "isError": isError}
}

func boolArg(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, _ := strconv.ParseBool(strings.TrimSpace(t))
		return b
	default:
		return false
	}
}

func stringArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func int64Arg(args map[string]any, key string) (int64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		v, err := t.Int64()
		return v, err == nil
	case string:
		if strings.TrimSpace(t) == "" {
			return 0, false
		}
		v, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func floatArg(args map[string]any, key string) (float64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		v, err := t.Float64()
		return v, err == nil
	case string:
		if strings.TrimSpace(t) == "" {
			return 0, false
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func listArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []string:
		return strings.Join(t, ",")
	case []any:
		parts := []string{}
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(trimmed) == "" {
		return readMessage(r)
	}
	if strings.HasPrefix(strings.TrimSpace(trimmed), "{") {
		return []byte(trimmed), nil
	}

	contentLength := -1
	for {
		name, value, ok := strings.Cut(trimmed, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			v, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || v < 0 {
				return nil, fmt.Errorf("invalid Content-Length header: %q", value)
			}
			contentLength = v
		}
		line, err = r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed = strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	msg := make([]byte, contentLength)
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func writeMessage(w io.Writer, v any) error {
	msg, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", msg)
	return err
}
