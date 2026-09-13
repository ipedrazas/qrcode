package httpapi

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestNoOutboundConnections serves QR codes for URLs that point at a local
// listener and at hostnames needing DNS, with every Go-level egress path
// trapped. The listener must never see a connection and no trap may fire.
//
// It swaps process-wide globals, so it must not run in parallel.
func TestNoOutboundConnections(t *testing.T) { //nolint:paralleltest // mutates http.DefaultTransport and net.DefaultResolver
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()

	var trapped atomic.Int32
	origTransport, origResolver := http.DefaultTransport, net.DefaultResolver
	http.DefaultTransport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		trapped.Add(1)
		return nil, errors.New("egress blocked by test")
	})
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		trapped.Add(1)
		return nil, errors.New("DNS blocked by test")
	}}
	t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = origTransport, origResolver })

	addr := ln.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	targets := []string{
		"http://" + addr + "/",
		"https://" + addr + "/path?x=1",
		"http://localhost:" + port + "/",
		"https://egress-canary.invalid/",
		"http://user:pw@egress-canary.example:" + port + "/",
	}
	h := newHandler(t, Config{})
	for _, target := range targets {
		if w := serve(h, http.MethodGet, "/qr", q("url", target)); w.Code != http.StatusOK {
			t.Fatalf("status %d for a valid URL: %s", w.Code, w.Body)
		}
	}

	// Give any stray background goroutine a moment to reveal itself.
	time.Sleep(100 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Errorf("listener accepted %d connection(s): the service dereferenced a URL", n)
	}
	if n := trapped.Load(); n != 0 {
		t.Errorf("%d outbound HTTP or DNS attempt(s) trapped", n)
	}
}

// TestNoNetworkClientCode scans every non-test Go file in the module for
// identifiers that make outbound connections. Serving HTTP is fine; being an
// HTTP client, dialing or resolving is not.
func TestNoNetworkClientCode(t *testing.T) {
	t.Parallel()
	forbidden := map[string]map[string]bool{
		"net/http": set("Get", "Head", "Post", "PostForm", "NewRequest", "NewRequestWithContext",
			"Client", "DefaultClient", "Transport", "DefaultTransport", "ProxyFromEnvironment"),
		"net": set("Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer",
			"LookupHost", "LookupIP", "LookupAddr", "LookupCNAME", "LookupMX", "LookupNS",
			"LookupTXT", "LookupSRV", "LookupPort", "Resolver", "DefaultResolver"),
	}
	forbiddenImports := set("net/http/httputil", "net/rpc", "net/smtp", "net/http/cookiejar", "crypto/tls", "os/exec")

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		checked++
		names := map[string]string{} // local name -> import path
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if forbiddenImports[p] {
				t.Errorf("%s imports %s", fset.Position(imp.Pos()), p)
			}
			if _, ok := forbidden[p]; ok {
				name := filepath.Base(p)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				names[name] = p
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok {
				if p, ok := names[x.Name]; ok && forbidden[p][sel.Sel.Name] {
					t.Errorf("%s uses %s.%s", fset.Position(sel.Pos()), p, sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 5 {
		t.Fatalf("only %d source files scanned; is the module root right?", checked)
	}
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
