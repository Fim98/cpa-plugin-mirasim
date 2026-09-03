package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	defaultListenAddress = "127.0.0.1:18317"
	defaultResourceBase  = "/v0/resource/plugins/mirasim"
)

func main() {
	listenAddress := flag.String("listen", defaultListenAddress, "loopback address used by the browser callback")
	upstreamRaw := flag.String("upstream", "", "public HTTPS origin of the remote CLIProxyAPI server")
	resourceBase := flag.String("resource-base", defaultResourceBase, "Mirasim plugin resource base path")
	flag.Parse()

	if errValidate := validateListenAddress(*listenAddress); errValidate != nil {
		log.Fatal(errValidate)
	}
	target, errTarget := parseUpstream(*upstreamRaw)
	if errTarget != nil {
		log.Fatal(errTarget)
	}
	paths, errPaths := oauthResourcePaths(*resourceBase)
	if errPaths != nil {
		log.Fatal(errPaths)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           newBridgeHandler(target, transport, paths),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	listener, errListen := net.Listen("tcp", server.Addr)
	if errListen != nil {
		log.Fatalf("listen on %s: %v", server.Addr, errListen)
	}
	log.Printf("Mirasim OAuth bridge listening on http://%s", listener.Addr())
	log.Printf("forwarding only Mirasim OAuth resources to %s", target.String())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if errServe := server.Serve(listener); errServe != nil && !errors.Is(errServe, http.ErrServerClosed) {
		log.Fatalf("serve OAuth bridge: %v", errServe)
	}
}

func newBridgeHandler(target *url.URL, transport http.RoundTripper, allowedPaths map[string]struct{}) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = target.Host
		for _, name := range []string{
			"Authorization",
			"Cookie",
			"Proxy-Authorization",
			"Referer",
			"X-Api-Key",
		} {
			req.Header.Del(name)
		}
	}
	proxy.Transport = transport
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Mirasim OAuth bridge could not reach CLIProxyAPI", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("Cache-Control", "no-store")
		return nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := allowedPaths[req.URL.Path]; !ok {
			http.NotFound(w, req)
			return
		}
		proxy.ServeHTTP(w, req)
	})
}

func validateListenAddress(value string) error {
	host, port, errSplit := net.SplitHostPort(strings.TrimSpace(value))
	if errSplit != nil || strings.TrimSpace(port) == "" {
		return fmt.Errorf("OAuth bridge listen address must be a loopback host and port")
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("OAuth bridge refuses non-loopback listen address %q", value)
	}
	return nil
}

func parseUpstream(value string) (*url.URL, error) {
	parsed, errParse := url.Parse(strings.TrimSpace(value))
	if errParse != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid OAuth bridge upstream URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("OAuth bridge upstream must use HTTP or HTTPS")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("OAuth bridge upstream must use HTTPS unless it is loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

func oauthResourcePaths(value string) (map[string]struct{}, error) {
	base := "/" + strings.Trim(strings.TrimSpace(value), "/")
	if base == "/" || strings.Contains(base, "..") || strings.ContainsAny(base, "?#") {
		return nil, fmt.Errorf("invalid Mirasim OAuth resource base path")
	}
	return map[string]struct{}{
		base + "/oauth/start":    {},
		base + "/oauth/callback": {},
	}, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
