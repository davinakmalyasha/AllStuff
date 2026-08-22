package main

import (
	"context"
	"errors"
	"flag"
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
	"bizverse/api/internal/service"
	"bizverse/api/internal/ws"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "run pending migrations and exit")
	flag.Parse()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		logger.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

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
	trendingSvc := service.NewTrending(repos)
	chatSvc := service.NewChat(repos, notifier)
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

	go jobs.Run(ctx, logger, repos, cfg, sender)

	go func() {
		logger.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
