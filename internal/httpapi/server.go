package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/RidhuanDEV/golang-backend/internal/cache"
	"github.com/RidhuanDEV/golang-backend/internal/config"
	"github.com/RidhuanDEV/golang-backend/internal/ratelimit"
	"github.com/RidhuanDEV/golang-backend/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type requestIDKey struct{}
type statusWriter struct {
	http.ResponseWriter
	status     int
	wrote      bool
	endpointID string
	actorID    string
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func requestID(ctx context.Context) string { v, _ := ctx.Value(requestIDKey{}).(string); return v }

type Server struct {
	Config     config.Config
	DB         *pgxpool.Pool
	Redis      *redis.Client
	Service    *Service
	Storage    storage.Storage
	Cache      *cache.Cache
	Limiter    *ratelimit.Limiter
	Policies   map[EndpointID]Endpoint
	Router     chi.Router
	Docs       huma.API
	registered map[EndpointID]struct{}
	Logger     *slog.Logger
}

func NewServer(c config.Config, pool *pgxpool.Pool, client *redis.Client, store storage.Storage, logger *slog.Logger) (*Server, error) {
	policies, err := Resolve(c)
	if err != nil {
		return nil, err
	}
	r := chi.NewRouter()
	docsRouter := chi.NewRouter()
	docsCfg := huma.DefaultConfig("Modular Go Backend", "1.0.0")
	docsCfg.OpenAPIPath = ""
	docsCfg.DocsPath = ""
	docsCfg.SchemasPath = ""
	docsCfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"}}
	s := &Server{Config: c, DB: pool, Redis: client, Service: &Service{DB: pool, Secret: []byte(c.JWTSecret), Log: logger}, Storage: store, Cache: cache.New(client, c.CacheEnabled), Limiter: ratelimit.New(func() *redis.Client {
		if c.RateStore == "redis" {
			return client
		}
		return nil
	}()), Policies: policies, Router: r, Docs: humachi.New(docsRouter, docsCfg), registered: map[EndpointID]struct{}{}, Logger: logger}
	r.Use(s.middleware)
	s.mount()
	if len(s.registered) != len(Definitions) {
		return nil, fmt.Errorf("registry coverage: mounted %d of %d endpoints", len(s.registered), len(Definitions))
	}
	return s, nil
}
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		tracked := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 128 {
			random := make([]byte, 16)
			if _, err := rand.Read(random); err == nil {
				id = hex.EncodeToString(random)
			} else {
				id = fmt.Sprintf("%d", time.Now().UnixNano())
			}
		}
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if origin := r.Header.Get("Origin"); origin != "" {
			if _, ok := s.Config.CORSOrigins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.Logger.Error("panic recovered", "error", recovered, "requestId", id)
				writeError(tracked, internal())
			}
			s.Logger.Info("http request", "method", r.Method, "path", r.URL.Path, "status", tracked.status, "endpointId", tracked.endpointID, "actorId", tracked.actorID, "requestId", id, "durationMs", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(tracked, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.Config.TrustProxyHops > 0 {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		if len(parts) >= s.Config.TrustProxyHops {
			candidate := strings.TrimSpace(parts[len(parts)-s.Config.TrustProxyHops])
			if parsed := net.ParseIP(candidate); parsed != nil {
				return parsed.String()
			}
		}
	}
	return host
}
func decodeJSON(r *http.Request, dst any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return badRequest("Content-Type must be application/json")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return badRequest("Invalid JSON body")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return badRequest("Only one JSON value is allowed")
	}
	return nil
}

type docsOutput[T any] struct{ Body Success[T] }

func register[I, T any](s *Server, id EndpointID, handler func(context.Context, *http.Request, *Actor) (Success[T], error)) {
	ep, exists := s.Policies[id]
	if !exists {
		panic("unregistered endpoint: " + id)
	}
	if _, duplicate := s.registered[id]; duplicate {
		panic("duplicate endpoint registration: " + id)
	}
	s.registered[id] = struct{}{}
	op := huma.Operation{OperationID: string(id), Method: ep.Method, Path: ep.Path, Summary: ep.Summary, Tags: []string{ep.Module}, DefaultStatus: ep.Status, Errors: []int{400, 401, 403, 404, 409, 429, 500}, Extensions: map[string]any{"x-audit-mode": ep.Audit, "x-rate-group": ep.Rate, "x-cache-mode": ep.Cache}}
	if !ep.Public {
		op.Security = []map[string][]string{{"bearer": {}}}
	}
	huma.Register(s.Docs, op, func(context.Context, *I) (*docsOutput[T], error) { return nil, nil })
	s.Router.MethodFunc(ep.Method, ep.Path, func(w http.ResponseWriter, r *http.Request) {
		if tracked, ok := w.(*statusWriter); ok {
			tracked.endpointID = string(id)
		}
		ctx := r.Context()
		var actor *Actor
		if !ep.Public {
			var err error
			actor, err = s.Service.Authenticate(ctx, r.Header.Get("Authorization"))
			if err != nil {
				writeError(w, err)
				return
			}
			if err = s.Service.Authorize(ctx, actor, ep.Permission); err != nil {
				writeError(w, err)
				return
			}
			if tracked, ok := w.(*statusWriter); ok {
				tracked.actorID = actor.ID
			}
		}
		if id != "health.get" && id != "live.get" && id != "ready.get" {
			key := s.clientIP(r)
			if actor != nil && ep.Rate == RateInternal {
				key = actor.ID
			}
			allowed, err := s.Limiter.Allow(ctx, string(ep.Rate), key, s.Config.Rates[string(ep.Rate)])
			if err != nil {
				s.Logger.Warn("rate store unavailable", "endpoint", id, "error", err)
				if ep.Rate == RateAuth {
					writeError(w, &APIError{Status: 503, Message: "Rate limiter unavailable", Errors: []string{}})
					return
				}
			} else if !allowed {
				writeError(w, &APIError{Status: 429, Message: "Too many requests", Errors: []string{}})
				return
			}
		}
		cacheKey := ""
		if ep.Cache == CacheRead && ep.Method == http.MethodGet {
			var err error
			who := "public"
			if actor != nil {
				who = actor.ID
			}
			cacheKey, err = s.Cache.Key(ctx, string(id), who, r.URL.RequestURI())
			if err != nil {
				s.Logger.Warn("cache key unavailable", "error", err)
			} else if cacheKey != "" {
				data, readErr := s.Cache.Get(ctx, cacheKey)
				if readErr != nil {
					s.Logger.Warn("cache read unavailable", "error", readErr)
				} else if data != nil {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(ep.Status)
					_, _ = w.Write(data)
					s.Service.ReadAudit(ctx, ep, actor, chi.URLParam(r, "id"))
					return
				}
			}
		}
		response, err := handler(ctx, r, actor)
		if err != nil {
			writeError(w, err)
			return
		}
		if ep.Method != http.MethodGet {
			if err = s.Cache.Invalidate(ctx); err != nil {
				s.Logger.Warn("cache invalidation failed", "error", err)
			}
		}
		if ep.Status == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := json.Marshal(response)
		if err != nil {
			writeError(w, internal())
			return
		}
		body = append(body, '\n')
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(ep.Status)
		_, _ = w.Write(body)
		if cacheKey != "" {
			if err = s.Cache.Put(ctx, cacheKey, body); err != nil {
				s.Logger.Warn("cache write unavailable", "error", err)
			}
		}
		if ep.Method == http.MethodGet {
			s.Service.ReadAudit(ctx, ep, actor, chi.URLParam(r, "id"))
		}
	})
}

func manual[I, O any](s *Server, id EndpointID, handler http.HandlerFunc) {
	ep := s.Policies[id]
	if _, exists := s.registered[id]; exists {
		panic("duplicate endpoint registration")
	}
	s.registered[id] = struct{}{}
	huma.Register(s.Docs, huma.Operation{OperationID: string(id), Method: ep.Method, Path: ep.Path, Summary: ep.Summary, Tags: []string{ep.Module}, DefaultStatus: ep.Status}, func(context.Context, *I) (*O, error) { return nil, nil })
	s.Router.MethodFunc(ep.Method, ep.Path, func(w http.ResponseWriter, r *http.Request) {
		if tracked, ok := w.(*statusWriter); ok {
			tracked.endpointID = string(id)
		}
		key := s.clientIP(r)
		allowed, err := s.Limiter.Allow(r.Context(), string(ep.Rate), key, s.Config.Rates[string(ep.Rate)])
		if err != nil {
			s.Logger.Warn("rate store unavailable", "endpoint", id, "error", err)
			if ep.Rate == RateAuth {
				writeError(w, &APIError{Status: 503, Message: "Rate limiter unavailable", Errors: []string{}})
				return
			}
		} else if !allowed {
			writeError(w, &APIError{Status: 429, Message: "Too many requests", Errors: []string{}})
			return
		}
		handler(w, r)
		if ep.Audit == AuditOptional {
			s.Service.ReadAudit(r.Context(), ep, nil, "")
		}
	})
}
