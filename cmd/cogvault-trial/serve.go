package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/httpauth"
	"github.com/teslamint/cogvault/internal/trial"
	"github.com/teslamint/cogvault/internal/trialmcp"
)

const bearerEnvironment = "COGVAULT_TRIALMCP_BEARER_TOKEN"

func serve(cfg trial.Config, args, environ []string, in io.Reader, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	transport := fs.String("transport", "stdio", "stdio or authenticated http")
	listen := fs.String("listen", "127.0.0.1:8081", "HTTP bind address")
	public := fs.String("public-url", "", "pathless public HTTPS URL")
	endpoint := fs.String("endpoint-path", "/mcp", "exact HTTP endpoint path")
	mode := fs.String("auth-mode", "", "bearer or oauth (HTTP only)")
	issuer := fs.String("oauth-issuer", "", "existing HTTPS OAuth issuer")
	audience := fs.String("oauth-audience", "", "distinct trial resource URL (public URL plus endpoint path)")
	scopes := fs.String("oauth-scopes", "", "space-separated required trial scopes")
	if err := fs.Parse(args); err != nil {
		return orHelp(err)
	}
	if fs.NArg() != 0 {
		return errors.New("serve accepts flags only")
	}
	token := ""
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, "COGVAULT_TRIALMCP_") {
			continue
		}
		if k != bearerEnvironment {
			return errors.New("unknown trial MCP environment setting")
		}
		token = v
	}
	var handlerCfg httpauth.Config
	switch *transport {
	case "stdio":
		invalid := token != ""
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "transport" {
				invalid = true
			}
		})
		if invalid {
			return errors.New("stdio refuses HTTP and credential settings")
		}
	case "http":
		handlerCfg = httpauth.Config{Mode: *mode, BearerToken: token, PublicURL: *public, EndpointPath: *endpoint, Issuer: *issuer, RequiredScopes: strings.Fields(*scopes)}
		if *mode == "oauth" {
			if *issuer == "" || *audience == "" || *audience != *public+*endpoint {
				return errors.New("OAuth audience must be the distinct trial public endpoint URL")
			}
			handlerCfg.Validator = httpauth.NewOAuthValidator(*issuer, *audience, handlerCfg.RequiredScopes, httpauth.NewJWKSCache(*issuer, 15*time.Minute, nil))
		} else if *audience != "" {
			return errors.New("OAuth audience is valid only in oauth mode")
		}
		if err := trialmcp.ValidateHTTPConfig(handlerCfg); err != nil {
			return err
		}
		if host, port, err := net.SplitHostPort(*listen); err != nil || port == "" || host == "" {
			return errors.New("HTTP listen must include an explicit host and port")
		}
	default:
		return errors.New("transport must be stdio or http")
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	m := trialmcp.NewServer(s)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *transport == "stdio" {
		return serveStdio(ctx, m, in, out)
	}
	h, err := trialmcp.NewHTTPHandler(m, handlerCfg)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("trial HTTP server stopped")
	}
	return nil
}

// Bound each line before handing it to mcp-go, whose stdio reader otherwise
// grows without limit. This is a local pipe only; it never opens a socket.
func serveStdio(ctx context.Context, s *server.MCPServer, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return server.NewStdioServer(s).Listen(ctx, &boundedLines{reader: bufio.NewReader(in)}, out)
}

type boundedLines struct {
	reader  *bufio.Reader
	pending []byte
}

func (b *boundedLines) Read(p []byte) (int, error) {
	if len(b.pending) == 0 {
		var line []byte
		for {
			part, prefix, err := b.reader.ReadLine()
			if len(line)+len(part) > trialmcp.MaxRequestBytes {
				return 0, errors.New("MCP input exceeds message limit")
			}
			line = append(line, part...)
			if err != nil {
				return 0, err
			}
			if !prefix {
				break
			}
		}
		b.pending = append(line, '\n')
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}
