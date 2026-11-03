package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/loopingz/webda.cli/webdaclient"
	"github.com/spf13/cobra"
)

// mcpProtocolVersions are the MCP revisions this server speaks, newest first.
var mcpProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// maxMCPLine bounds one JSON-RPC message read from stdin.
const maxMCPLine = 16 << 20

// mcpTool is an operation served as an MCP tool.
type mcpTool struct {
	op     Operation
	schema map[string]any
}

// mcpServer serves the profile's operations as MCP tools over stdio: each call is an operation request through the
// CLI client, so it carries the profile's OAuth token and device unlock; the MCP client never holds a credential.
type mcpServer struct {
	name, version string
	client        *webdaclient.Client
	baseURL       string
	tools         map[string]*mcpTool
	order         []string
	out           io.Writer
	// timeout bounds each tool call (0: none)
	timeout time.Duration
	mu      sync.Mutex // serializes writes to out
}

// toolPatterns splits a comma-separated glob list ("Forge.*,Task.Get").
func toolPatterns(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// newMCPServer selects the tools: the include patterns (all when none), minus the excluded ones and the server's
// device unlock plumbing, which an agent must never drive.
func newMCPServer(name, version string, client *webdaclient.Client, baseURL string, ops []Operation, du *DeviceUnlock, include, exclude []string, out io.Writer) *mcpServer {
	s := &mcpServer{name: name, version: version, client: client, baseURL: baseURL, tools: map[string]*mcpTool{}, out: out}
	if du != nil {
		exclude = append(exclude, du.Request, du.Status, du.Lock)
	}
	for i := range ops {
		op := ops[i]
		if op.Name == "" || (len(include) > 0 && !matchesAny(op.Name, include)) || matchesAny(op.Name, exclude) {
			continue
		}
		if _, dup := s.tools[op.Name]; dup {
			continue
		}
		t := &mcpTool{op: op, schema: op.Input}
		// MCP tools take an object, as operation requests do
		if typ, _ := t.schema["type"].(string); typ != "object" {
			t.schema = map[string]any{"type": "object"}
		}
		s.tools[op.Name] = t
		s.order = append(s.order, op.Name)
	}
	sort.Strings(s.order)
	return s
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *mcpServer) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
}

func (s *mcpServer) reply(id json.RawMessage, result any) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *mcpServer) fail(id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: message}})
}

// serve reads newline-delimited JSON-RPC messages until in closes or ctx ends; tool calls run concurrently.
func (s *mcpServer) serve(ctx context.Context, in io.Reader) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), maxMCPLine)
	var wg sync.WaitGroup
	defer wg.Wait()
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.JSONRPC != "2.0" {
			s.fail(nil, -32700, "Parse error")
			continue
		}
		if len(msg.ID) == 0 || msg.Method == "" {
			// a notification (initialized, progress...) or a response: nothing to answer
			continue
		}
		if msg.Method == "tools/call" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.handle(ctx, msg)
			}()
			continue
		}
		s.handle(ctx, msg)
	}
	return scanner.Err()
}

func (s *mcpServer) handle(ctx context.Context, msg rpcMessage) {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		version := mcpProtocolVersions[0]
		for _, v := range mcpProtocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		s.reply(msg.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
		})
	case "ping":
		s.reply(msg.ID, map[string]any{})
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.order))
		for _, name := range s.order {
			t := s.tools[name]
			tool := map[string]any{"name": name, "inputSchema": t.schema}
			if d := firstNonEmpty(t.op.Summary, t.op.Description); d != "" {
				tool["description"] = d
			}
			if isDestructive(t.op) {
				tool["annotations"] = map[string]any{"destructiveHint": true}
			}
			tools = append(tools, tool)
		}
		s.reply(msg.ID, map[string]any{"tools": tools})
	case "tools/call":
		s.call(ctx, msg)
	default:
		s.fail(msg.ID, -32601, "Method not found: "+msg.Method)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// call runs the operation; an HTTP or validation error is a tool error (isError) the model can read.
func (s *mcpServer) call(ctx context.Context, msg rpcMessage) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.fail(msg.ID, -32602, "Invalid params")
		return
	}
	t, ok := s.tools[p.Name]
	if !ok {
		s.fail(msg.ID, -32602, "Unknown tool: "+p.Name)
		return
	}
	body := p.Arguments
	if body == nil {
		body = map[string]any{}
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	// an unlock made (or locked) by another process since the start
	s.client.ReloadUnlock()
	data, err := executeOperation(ctx, t.op, s.client, s.baseURL, body)
	if err != nil {
		s.reply(msg.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}})
		return
	}
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": string(data)}}}
	var structured map[string]any
	if json.Unmarshal(data, &structured) == nil {
		result["structuredContent"] = structured
	}
	s.reply(msg.ID, result)
}

func newMCPCmd(profile string, prof Profile, client *webdaclient.Client, baseURL string, ops []Operation, du *DeviceUnlock, in io.Reader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve this profile's operations as MCP tools over stdio",
		Long: "Runs a local MCP server (stdio) for MCP clients such as Claude Code or Codex. Each tool is an operation " +
			"of this profile, called with the profile's sign-in and vault unlock: the MCP client never holds a credential.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			include, _ := cmd.Flags().GetString("tools")
			exclude, _ := cmd.Flags().GetString("exclude")
			s := newMCPServer(profile, version, client, baseURL, ops, du, toolPatterns(include), toolPatterns(exclude), cmd.OutOrStdout())
			s.timeout, _ = cmd.Flags().GetDuration("timeout")
			stderr := cmd.ErrOrStderr()
			if watch, _ := cmd.Flags().GetBool("lock-on-screen-lock"); watch {
				if du == nil {
					return fmt.Errorf("--lock-on-screen-lock: the server advertises no vault unlock")
				}
				if err := screenSupported(cmd.Context()); err != nil {
					return fmt.Errorf("--lock-on-screen-lock: %w", err)
				}
				endpoint := operationsEndpoint(ops, baseURL)
				go watchScreenForMCP(cmd.Context(), client, du, endpoint, func(m string) { _, _ = fmt.Fprintln(stderr, m) })
			}
			_, _ = fmt.Fprintf(stderr, "Serving %d tools of %s over MCP (stdio)\n", len(s.order), profile)
			return s.serve(cmd.Context(), in)
		},
	}
	cmd.Flags().String("tools", "", "comma-separated globs of the operations to serve (default: all)")
	cmd.Flags().String("exclude", "", "comma-separated globs of the operations not to serve")
	cmd.Flags().Bool("lock-on-screen-lock", prof.LockOnScreenLock, "lock the vault unlock whenever the screen locks while serving (macOS, Linux)")
	return cmd
}
