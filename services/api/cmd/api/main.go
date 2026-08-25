package main

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/db"
	"bizverse/api/internal/email"
	"bizverse/api/internal/httpapi"
	"bizverse/api/internal/jobs"
	"bizverse/api/internal/ratelimit"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/security"
	"bizverse/api/internal/service"
	"bizverse/api/internal/ws"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "run pending migrations and exit")
	vapidGen := flag.Bool("vapid", false, "generate a VAPID keypair for web push and exit")
	flag.Parse()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		logger.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if *vapidGen {
		kp, err := security.GenerateVAPIDKeypair()
		if err != nil {
			logger.Error("vapid keygen", "err", err)
			os.Exit(1)
		}
		der, err := x509.MarshalPKCS8PrivateKey(kp.PrivateKey)
		if err != nil {
			logger.Error("vapid marshal", "err", err)
			os.Exit(1)
		}
		fmt.Println("VAPID_PUBLIC_KEY=" + kp.PublicKey)
		fmt.Println("VAPID_PRIVATE_KEY=" + base64.RawURLEncoding.EncodeToString(der))
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connect", "err", err)
		os.Exit(1)
	}
	// NOTE: pool.Close is called explicitly in the shutdown sequence below —
	// a deferred Close ran BEFORE ctx cancellation here (LIFO), killing
	// in-flight queries and jobs instead of letting them drain.

	if err := db.Migrate(ctx, pool, cfg.MigrationsDir); err != nil {
		logger.Error("migrations", "err", err)
		os.Exit(1)
	}
	if *migrateOnly {
		logger.Info("migrations applied")
		return
	}

	repos := repo.New(pool)
	sender := email.NewSender(email.ResendConfig{
		APIKey:    cfg.ResendAPIKey,
		From:      cfg.EmailFrom,
		AppEnv:    cfg.AppEnv,
		PublicURL: cfg.PublicURL,
		SMTPAddr:  cfg.SMTPAddr,
		SMTPUser:  cfg.SMTPUser,
		SMTPPass:  cfg.SMTPPass,
	})
	rateLimiter := ratelimit.NewInMemory()
	wsOrigins := append(append([]string{}, cfg.WSOrigins...), cfg.CORSOrigins...)
	var pubsub *ws.RedisPubSub
	if cfg.RedisURL != "" {
		pubsub, err = ws.NewRedisPubSub(cfg.RedisURL, "bizverse:ws", logger)
		if err != nil {
			logger.Warn("redis pubsub disabled", "err", err)
			pubsub = nil
		} else {
			logger.Info("ws fan-out via redis", "addr", cfg.RedisURL)
		}
	}
	hub := ws.NewHubWithRedis(logger, wsOrigins, pubsub)
	go hub.Run(ctx)

	notifier := service.NewNotifier(repos, cfg, sender, hub, logger)
	authSvc := service.NewAuth(repos, cfg, sender, logger)
	userSvc := service.NewUsers(repos)
	categorySvc := service.NewCategories(repos)
	businessSvc := service.NewBusinesses(repos)
	productSvc := service.NewProducts(repos, notifier)
	analyticsSvc := service.NewAnalytics(repos)
	engagementSvc := service.NewEngagement(repos, notifier)
	trendingSvc := service.NewTrending(repos, notifier)
	chatSvc := service.NewChat(repos, notifier)
	// Thread membership gate for WS `subscribe` frames: without it any
	// authenticated user could subscribe to arbitrary thread IDs and receive
	// typing/presence signals for conversations they are not part of.
	hub.SetAuthorizeSubscribe(func(userID string, threadIDs []string) []string {
		out := make([]string, 0, len(threadIDs))
		for _, id := range threadIDs {
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := chatSvc.CheckAccess(cctx, userID, id); err == nil {
				out = append(out, id)
			}
			cancel()
		}
		return out
	})
	currencySvc := service.NewCurrency(repos)
	oauthSvc := service.NewOAuth(repos, cfg)
	communitySvc := service.NewCommunity(repos, notifier)
	profilesSvc := service.NewProfiles(repos, notifier)
	invitesSvc := service.NewInvites(repos)
	searchSvc := service.NewSearch(repos)
	adminSvc := service.NewAdmin(repos, notifier)
	mediaSvc := service.NewMedia(repos, cfg.MediaDir, cfg.MediaBase, os.Getenv("MEDIA_ENCRYPTION_KEY"), os.Getenv("CLAMAV_ADDR"))
	citiesSvc := service.NewCities(repos)
	apiKeysSvc := service.NewAPIKeys(repos)
	claimsSvc := service.NewClaims(repos, notifier)

	api := httpapi.NewServer(httpapi.Deps{
		Config:      cfg,
		Logger:      logger,
		Repos:       repos,
		Auth:        authSvc,
		Users:       userSvc,
		Categories:  categorySvc,
		Businesses:  businessSvc,
		Products:    productSvc,
		Analytics:   analyticsSvc,
		Engagement:  engagementSvc,
		Trending:    trendingSvc,
		Chat:        chatSvc,
		Currency:    currencySvc,
		OAuth:       oauthSvc,
		Community:   communitySvc,
		Profiles:    profilesSvc,
		Invites:     invitesSvc,
		Search:      searchSvc,
		Admin:       adminSvc,
		Media:       mediaSvc,
		Cities:      citiesSvc,
		APIKeys:     apiKeysSvc,
		Claims:      claimsSvc,
		Notifier:    notifier,
		RateLimiter: rateLimiter,
		Hub:         hub,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go jobs.Run(ctx, logger, repos, cfg, sender, notifier)

	go func() {
		logger.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	// Ordered shutdown: 1) drain HTTP (hijacked WS conns survive Shutdown),
	// 2) cancel ctx → hub closes live sockets, jobs stop, 3) pool last so
	// nothing touches a closed pool mid-drain.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "err", err)
	}
	stop()
	pool.Close()
}
