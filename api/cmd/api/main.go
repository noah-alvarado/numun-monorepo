// Command api is the NUMUN portal Lambdalith.
//
// In production it runs as a single AWS Lambda function fronted by API Gateway
// HTTP API. Locally it is invoked by `sam local start-api` or — when the
// LOCAL_HTTP env var is set — runs as a plain net/http server on port 3000
// for quick iteration outside of Docker.
//
// See APPLICATION.md §4 and API.md §1.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/cmsoauth"
	healthv1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/gen/numun/v1/numunv1connect"
	"github.com/numun/numun/api/internal/handlers"
	"github.com/numun/numun/api/internal/store"
)

// Build metadata stamped in at link time via -ldflags (CI) and
// optionally overridden at runtime via env vars (SAM Environment.Variables).
var (
	commit  = "dev"
	version = "0.0.0"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if v := os.Getenv("COMMIT_SHA"); v != "" {
		commit = v
	}
	if v := os.Getenv("RELEASE_VERSION"); v != "" {
		version = v
	}

	ctx := context.Background()
	st, err := store.New(ctx)
	if err != nil {
		logger.Error("init store", "err", err)
		os.Exit(1)
	}
	cog, err := auth.NewCognito(ctx)
	if err != nil {
		logger.Error("init cognito", "err", err)
		os.Exit(1)
	}
	verifier := auth.NewVerifier(cog.Region, cog.UserPoolID)

	root := buildHandler(logger, st, cog, verifier)

	if os.Getenv("LOCAL_HTTP") == "true" {
		addr := ":3000"
		logger.Info("starting local HTTP server", "addr", addr)
		if err := http.ListenAndServe(addr, root); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server exited", "err", err)
			os.Exit(1)
		}
		return
	}

	lambda.Start(httpadapter.NewV2(root).ProxyWithContext)
}

func buildHandler(logger *slog.Logger, st *store.Client, cog *auth.Cognito, ver *auth.Verifier) http.Handler {
	mux := http.NewServeMux()

	// Plain HTTP probe — usable by curl, ALBs, and uptime checks.
	mux.HandleFunc("GET /v1/health", handleHealthHTTP)

	// Decap CMS GitHub OAuth proxy — see docs/subsystems/CMS_CONTENT_MODEL.md §8.3.
	// Mounted outside the Connect router and outside the auth middleware
	// (the public-paths allowlist below carves out /cms-oauth/*).
	cms := cmsoauth.New(context.Background(), logger)
	mux.HandleFunc("GET /cms-oauth/auth", cms.Auth)
	mux.HandleFunc("GET /cms-oauth/callback", cms.Callback)

	validate, err := handlers.NewValidationInterceptor()
	if err != nil {
		logger.Error("init validation interceptor", "err", err)
		os.Exit(1)
	}
	opts := connect.WithInterceptors(validate)
	scoper := auth.NewScoper(st)

	healthPath, healthHandler := numunv1connect.NewHealthServiceHandler(&healthServer{}, opts)
	mux.Handle(healthPath, healthHandler)

	authSvc := &handlers.AuthService{
		Store:    st,
		Cognito:  cog,
		Verifier: ver,
		Logger:   logger,
	}
	authPath, authHandler := numunv1connect.NewAuthServiceHandler(authSvc, opts)
	mux.Handle(authPath, authHandler)

	userSvc := &handlers.UserService{
		Store:   st,
		Cognito: cog,
		Logger:  logger,
	}
	userPath, userHandler := numunv1connect.NewUserServiceHandler(userSvc, opts)
	mux.Handle(userPath, userHandler)

	confSvc := &handlers.ConferenceService{Store: st, Scoper: scoper, Logger: logger}
	confPath, confHandler := numunv1connect.NewConferenceServiceHandler(confSvc, opts)
	mux.Handle(confPath, confHandler)

	delSvc := &handlers.DelegationService{Store: st, Scoper: scoper, Logger: logger}
	delPath, delHandler := numunv1connect.NewDelegationServiceHandler(delSvc, opts)
	mux.Handle(delPath, delHandler)

	pubSvc := &handlers.PublicService{Store: st, Logger: logger}
	pubPath, pubHandler := numunv1connect.NewPublicServiceHandler(pubSvc, opts)
	mux.Handle(pubPath, pubHandler)

	mw := auth.New(auth.MiddlewareConfig{
		Store:     st,
		Cognito:   cog,
		Logger:    logger,
		DevMode:   os.Getenv("DEV_MODE") == "true",
		DevBypass: os.Getenv("DEV_BYPASS_AUTH") == "true",
	})
	return mw(mux)
}

type healthServer struct{}

func (h *healthServer) Check(_ context.Context, _ *connect.Request[healthv1.CheckRequest]) (*connect.Response[healthv1.CheckResponse], error) {
	return connect.NewResponse(&healthv1.CheckResponse{
		Commit:  commit,
		Version: version,
	}), nil
}

func handleHealthHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"commit":  commit,
		"version": version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}
