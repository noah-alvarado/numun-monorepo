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
	"github.com/numun/numun/api/internal/cms"
	"github.com/numun/numun/api/internal/cmsoauth"
	"github.com/numun/numun/api/internal/email"
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

	emailSvc, err := email.New(ctx, st, logger)
	if err != nil {
		logger.Error("init email", "err", err)
		os.Exit(1)
	}

	// CMS git client (M11). When SSM credentials are missing — typical in
	// `make dev` — fall back to a stub that reports ok without network I/O so
	// award mutations still work locally; the public site won't update there.
	cmsClient := buildCMSClient(ctx, logger)

	root := buildHandler(logger, st, cog, verifier, emailSvc, cmsClient)

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

// buildCMSClient resolves the GitHub App config from SSM and constructs a
// real cms.Client; on any failure it returns a stub so make dev keeps working
// without GitHub App credentials. See IMPLEMENTATION_PLAN.md M11.
func buildCMSClient(ctx context.Context, logger *slog.Logger) *cms.Client {
	devMode := os.Getenv("DEV_MODE") == "true"
	cfg, err := cms.LoadConfigFromSSM(ctx, logger)
	if err != nil {
		if !devMode {
			logger.Warn("cms: SSM config missing in non-dev mode — award CMS sync disabled", "err", err)
		}
		return cms.NewStub(logger)
	}
	client, err := cms.New(cfg, logger)
	if err != nil {
		logger.Warn("cms: client init failed — using stub", "err", err)
		return cms.NewStub(logger)
	}
	return client
}

func buildHandler(logger *slog.Logger, st *store.Client, cog *auth.Cognito, ver *auth.Verifier, emailSvc *email.Client, cmsClient *cms.Client) http.Handler {
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

	delSvc := &handlers.DelegationService{Store: st, Scoper: scoper, Email: emailSvc, Logger: logger}
	delPath, delHandler := numunv1connect.NewDelegationServiceHandler(delSvc, opts)
	mux.Handle(delPath, delHandler)

	delegateSvc := &handlers.DelegateService{Store: st, Scoper: scoper, Email: emailSvc, Logger: logger}
	delegatePath, delegateHandler := numunv1connect.NewDelegateServiceHandler(delegateSvc, opts)
	mux.Handle(delegatePath, delegateHandler)

	uploadSvc := &handlers.UploadService{Store: st, Logger: logger}
	uploadPath, uploadHandler := numunv1connect.NewUploadServiceHandler(uploadSvc, opts)
	mux.Handle(uploadPath, uploadHandler)

	committeeSvc := &handlers.CommitteeService{Store: st, Scoper: scoper, Logger: logger}
	committeePath, committeeHandler := numunv1connect.NewCommitteeServiceHandler(committeeSvc, opts)
	mux.Handle(committeePath, committeeHandler)

	positionSvc := &handlers.PositionService{Store: st, Scoper: scoper, Logger: logger}
	positionPath, positionHandler := numunv1connect.NewPositionServiceHandler(positionSvc, opts)
	mux.Handle(positionPath, positionHandler)

	assignmentSvc := &handlers.AssignmentService{Store: st, Scoper: scoper, Email: emailSvc, Logger: logger}
	assignmentPath, assignmentHandler := numunv1connect.NewAssignmentServiceHandler(assignmentSvc, opts)
	mux.Handle(assignmentPath, assignmentHandler)

	assignmentRunSvc := &handlers.AssignmentRunService{Store: st, Scoper: scoper, Logger: logger}
	assignmentRunPath, assignmentRunHandler := numunv1connect.NewAssignmentRunServiceHandler(assignmentRunSvc, opts)
	mux.Handle(assignmentRunPath, assignmentRunHandler)

	paymentSvc := &handlers.PaymentService{Store: st, Scoper: scoper, Email: emailSvc, Logger: logger}
	paymentPath, paymentHandler := numunv1connect.NewPaymentServiceHandler(paymentSvc, opts)
	mux.Handle(paymentPath, paymentHandler)

	awardSvc := &handlers.AwardService{Store: st, Scoper: scoper, CMS: cmsClient, Logger: logger}
	awardPath, awardHandler := numunv1connect.NewAwardServiceHandler(awardSvc, opts)
	mux.Handle(awardPath, awardHandler)

	announcementSvc := &handlers.AnnouncementService{Store: st, Email: emailSvc, Logger: logger}
	announcementPath, announcementHandler := numunv1connect.NewAnnouncementServiceHandler(announcementSvc, opts)
	mux.Handle(announcementPath, announcementHandler)

	emailHealthSvc := &handlers.EmailHealthService{Store: st, Logger: logger}
	emailHealthPath, emailHealthHandler := numunv1connect.NewEmailHealthServiceHandler(emailHealthSvc, opts)
	mux.Handle(emailHealthPath, emailHealthHandler)

	// List-Unsubscribe one-click handler. Outside the Connect router; auth
	// middleware below carves out /v1/email/unsubscribe via the public-paths
	// allowlist so a logged-out click still works. EMAIL.md §2.2.
	unsubSvc := &handlers.UnsubscribeRoutes{Store: st, Cfg: emailSvc.Cfg, Logger: logger}
	unsubSvc.Register(mux)

	// CSV export surface (API.md §12.1): non-Connect HTTP routes mounted on
	// the same mux. Auth + CSRF middleware below covers them.
	exportRoutes := &handlers.ExportRoutes{Store: st, Scoper: scoper, Logger: logger}
	exportRoutes.Register(mux)

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
