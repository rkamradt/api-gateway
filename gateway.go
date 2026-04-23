package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return sw.ResponseWriter.Write(b)
}

type gateway struct {
	cfg       Config
	jwksCache *jwk.Cache
	jwksURL   string
	routes    []proxyRoute
}

type proxyRoute struct {
	prefix      string
	proxy       *httputil.ReverseProxy
	stripPrefix bool
}

func newGateway(ctx context.Context, cfg Config) (*gateway, error) {
	jwksURL := "https://" + cfg.Auth0Domain + "/.well-known/jwks.json"

	cache := jwk.NewCache(ctx)
	if err := cache.Register(jwksURL); err != nil {
		return nil, fmt.Errorf("registering JWKS URL: %w", err)
	}
	if _, err := cache.Refresh(ctx, jwksURL); err != nil {
		return nil, fmt.Errorf("initial JWKS fetch from %s: %w", jwksURL, err)
	}
	log.Printf("JWKS loaded from %s", jwksURL)

	gw := &gateway{
		cfg:       cfg,
		jwksCache: cache,
		jwksURL:   jwksURL,
	}

	for _, r := range cfg.Routes {
		upstream, err := url.Parse(r.Upstream)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream %q: %w", r.Upstream, err)
		}
		gw.routes = append(gw.routes, proxyRoute{
			prefix:      r.Prefix,
			proxy:       httputil.NewSingleHostReverseProxy(upstream),
			stripPrefix: r.StripPrefix,
		})
		log.Printf("route: %s -> %s (stripPrefix=%v)", r.Prefix, r.Upstream, r.StripPrefix)
	}

	return gw, nil
}

func (gw *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}

	sw := &statusWriter{ResponseWriter: w}
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		if sw.status == http.StatusNotFound {
			log.Printf("WARN  %s %s -> %d (%s) — no matching route", r.Method, r.URL.Path, sw.status, duration)
		} else {
			log.Printf("INFO  %s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, duration)
		}
	}()
	w = sw

	gw.addCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sub, err := gw.validateToken(r)
	if err != nil {
		log.Printf("auth rejected %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r = r.Clone(r.Context())
	r.Header.Set("X-User-ID", sub)

	for _, route := range gw.routes {
		if strings.HasPrefix(r.URL.Path, route.prefix) {
			if route.stripPrefix {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, route.prefix)
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
				if r.URL.RawPath != "" {
					r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, route.prefix)
				}
			}
			route.proxy.ServeHTTP(w, r)
			return
		}
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (gw *gateway) addCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	allowed := len(gw.cfg.CORSOrigins) == 0
	for _, o := range gw.cfg.CORSOrigins {
		if o == "*" || o == origin {
			allowed = true
			break
		}
	}

	if allowed {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
	}
}

func (gw *gateway) validateToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("missing or malformed Authorization header")
	}
	tokenStr := authHeader[len("Bearer "):]

	keySet, err := gw.jwksCache.Get(r.Context(), gw.jwksURL)
	if err != nil {
		return "", fmt.Errorf("fetching JWKS: %w", err)
	}

	token, err := jwt.Parse([]byte(tokenStr),
		jwt.WithKeySet(keySet),
		jwt.WithIssuer("https://"+gw.cfg.Auth0Domain+"/"),
		jwt.WithAudience(gw.cfg.Audience),
		jwt.WithValidate(true),
	)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	return token.Subject(), nil
}
