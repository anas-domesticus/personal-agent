package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// MCP is the parsed MCP file.
//
//	servers:
//	  composio:
//	    transport: http            # http (streamable HTTP), sse or stdio
//	    url: https://example.com/mcp
//	    headers:
//	      x-api-key: ${COMPOSIO_API_KEY}
//	    tools: [GMAIL_FETCH_EMAILS] # optional allowlist; default is every tool
//	  files:
//	    transport: stdio
//	    command: npx
//	    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
//	    env:
//	      LOG_LEVEL: warn
//	    disabled: true
//
// ${VAR} references in url, headers, command, args and env are filled from
// the environment, so secrets never live in the file. A server that
// references an unset variable is skipped with a warning.
type MCP struct {
	Servers map[string]MCPServer `yaml:"servers"`
}

type MCPServer struct {
	Transport string            `yaml:"transport"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	Tools     []string          `yaml:"tools"`
	Disabled  bool              `yaml:"disabled"`
	Timeout   time.Duration     `yaml:"timeout"` // connect timeout; default 30s

	// Set by Load after expanding ${VAR}s.
	Name    string   `yaml:"-"`
	Missing []string `yaml:"-"` // unset variables the server references
}

const (
	TransportHTTP  = "http"
	TransportSSE   = "sse"
	TransportStdio = "stdio"
)

// ServerNamePattern keeps tool names (<server>__<tool>) within what model
// APIs accept. Underscores aren't allowed, so the "__" separator is never
// ambiguous.
var ServerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)

// LoadMCP reads and validates an MCP file. A missing file is not an error.
func LoadMCP(path string) (MCP, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return MCP{}, nil
	}
	if err != nil {
		return MCP{}, fmt.Errorf("read %s: %w", path, err)
	}
	m, err := ParseMCP(raw, os.LookupEnv)
	if err != nil {
		return MCP{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// ParseMCP parses MCP YAML, expanding ${VAR}s with lookup.
func ParseMCP(raw []byte, lookup func(string) (string, bool)) (MCP, error) {
	var m MCP
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) { // EOF: empty file
		return MCP{}, err
	}

	var errs []error
	for name, s := range m.Servers {
		s.Name = name
		if !ServerNamePattern.MatchString(name) {
			errs = append(errs, fmt.Errorf("server %q: name must match %s", name, ServerNamePattern))
		}
		switch s.Transport {
		case TransportHTTP, TransportSSE:
			if s.URL == "" {
				errs = append(errs, fmt.Errorf("server %q: url is required for %s", name, s.Transport))
			}
			if s.Command != "" || len(s.Args) > 0 {
				errs = append(errs, fmt.Errorf("server %q: command and args are only for stdio", name))
			}
		case TransportStdio:
			if s.Command == "" {
				errs = append(errs, fmt.Errorf("server %q: command is required for stdio", name))
			}
			if s.URL != "" || len(s.Headers) > 0 {
				errs = append(errs, fmt.Errorf("server %q: url and headers are not used by stdio", name))
			}
		default:
			errs = append(errs, fmt.Errorf("server %q: transport must be http, sse or stdio", name))
		}
		if s.Timeout == 0 {
			s.Timeout = 30 * time.Second
		}

		missing := map[string]bool{}
		expand := func(v string) string {
			return os.Expand(v, func(key string) string {
				val, ok := lookup(key)
				if !ok {
					missing[key] = true
				}
				return val
			})
		}
		s.URL = expand(s.URL)
		s.Command = expand(s.Command)
		for i := range s.Args {
			s.Args[i] = expand(s.Args[i])
		}
		for k, v := range s.Headers {
			s.Headers[k] = expand(v)
		}
		for k, v := range s.Env {
			s.Env[k] = expand(v)
		}
		for k := range missing {
			s.Missing = append(s.Missing, k)
		}
		sort.Strings(s.Missing)

		m.Servers[name] = s
	}
	return m, errors.Join(errs...)
}

// Enabled returns the servers to connect to, sorted by name.
func (m MCP) Enabled() []MCPServer {
	var out []MCPServer
	for _, s := range m.Servers {
		if !s.Disabled {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
