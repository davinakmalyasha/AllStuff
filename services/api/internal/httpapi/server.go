package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/ratelimit"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/service"
	"bizverse/api/internal/ws"
)

// Deps wires the API server.
type Deps struct {
	Config      config.Config
	Logger      *slog.Logger
	Repos       *repo.Repos
	Auth        *service.Auth
	Users       *service.Users
	Categories  *service.Categories
	Businesses  *service.Businesses
	Products    *service.Products
	Analytics   *service.Analytics
	Engagement  *service.Engagement
	Trending    *service.Trending
	Chat        *service.Chat
	Currency    *service.Currency
	Search      *service.Search
	Admin       *service.Admin
	Media       *service.Media
	OAuth       *service.OAuth
	Community   *service.Community
	Profiles    *service.Profiles
	Invites     *service.Invites
	Cities      *service.Cities
	APIKeys     *service.APIKeys
	Claims      *service.Claims
	Notifier    *service.Notifier
	RateLimiter ratelimit.RateLimiter
	Hub         *ws.Hub
}

type Server struct {
	deps Deps
	mux  *http.ServeMux
	metrics Metrics
}

func NewServer(deps Deps) *Server {
	s := &Server{deps: deps, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	mux := s.mux

	// Health
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/ready", s.handleReady)
	// Ops metrics (Prometheus text format); admin-only in prod.
	mux.HandleFunc("GET /metrics", s.metricsGuard(s.handleMetrics))

	// Auth
	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/v1/auth/verify-email", s.handleVerifyEmail)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", s.handleForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", s.handleResetPassword)
	mux.HandleFunc("POST /api/v1/auth/2fa/verify", s.handle2FAVerify)
	mux.HandleFunc("GET /api/v1/auth/oauth/google", s.handleOAuthStart)
	mux.HandleFunc("GET /api/v1/auth/oauth/google/callback", s.handleOAuthCallback)
	mux.HandleFunc("GET /api/v1/auth/csrf", s.handleCSRF)

	// Security (2FA, sessions, export)
	mux.HandleFunc("GET /api/v1/me/security", s.handle2FAStatus)
	mux.HandleFunc("GET /api/v1/me/api-keys", s.handleAPIKeys)
	mux.HandleFunc("POST /api/v1/me/api-keys", s.handleAPIKeys)
	mux.HandleFunc("DELETE /api/v1/me/api-keys/{keyId}", s.handleAPIKeyRevoke)
	mux.HandleFunc("GET /api/v1/me/claims", s.handleClaims)
	mux.HandleFunc("POST /api/v1/claims", s.handleClaims)
	mux.HandleFunc("GET /api/v1/admin/claims", s.adminOnly(s.handleAdminClaims))
	mux.HandleFunc("POST /api/v1/admin/claims/{claimId}/decide", s.adminOnly(s.handleAdminClaims))
	mux.HandleFunc("POST /api/v1/me/security/2fa", s.handle2FAEnroll)
	mux.HandleFunc("POST /api/v1/me/security/2fa/confirm", s.handle2FAConfirm)
	mux.HandleFunc("DELETE /api/v1/me/security/2fa", s.handle2FADisable)
	mux.HandleFunc("GET /api/v1/me/security/sessions", s.handleSessions)
	mux.HandleFunc("DELETE /api/v1/me/security/sessions/{sessionId}", s.handleSessionRevoke)
	mux.HandleFunc("POST /api/v1/me/security/sessions/revoke-others", s.handleSessionRevokeOthers)
	mux.HandleFunc("GET /api/v1/me/export", s.handleExport)
	mux.HandleFunc("GET /api/v1/me/reviews", s.handleMyReviews)
	mux.HandleFunc("POST /api/v1/me/delete", s.handleDeleteAccount)
	mux.HandleFunc("POST /api/v1/me/delete/cancel", s.handleCancelDeletion)
	mux.HandleFunc("GET /api/v1/me/notifications-settings", s.handleNotificationPrefs)
	mux.HandleFunc("PATCH /api/v1/me/notifications-settings", s.handleNotificationPrefs)
	mux.HandleFunc("POST /api/v1/me/password", s.handleChangePassword)
	mux.HandleFunc("POST /api/v1/me/email", s.handleChangeEmail)
	mux.HandleFunc("POST /api/v1/me/security/2fa/recovery-codes", s.handleRegenerateRecovery)
	mux.HandleFunc("GET /api/v1/me/saved-searches", s.handleSavedSearches)
	mux.HandleFunc("POST /api/v1/me/saved-searches", s.handleSavedSearches)
	mux.HandleFunc("DELETE /api/v1/me/saved-searches/{searchId}", s.handleSavedSearchDelete)
	mux.HandleFunc("PATCH /api/v1/me/saved-searches/{searchId}", s.handleSavedSearchPatch)
	mux.HandleFunc("GET /api/v1/collections/{id}", s.handlePublicCollection)
	mux.HandleFunc("GET /api/v1/me/compare", s.handleCompareSync)
	mux.HandleFunc("PUT /api/v1/me/compare", s.handleCompareSync)
	mux.HandleFunc("POST /api/v1/errors", s.handleClientError)
	mux.HandleFunc("GET /api/v1/push/vapid-key", s.handleVapidKey)

	// Me
	mux.HandleFunc("GET /api/v1/me", s.handleMe)
	mux.HandleFunc("PATCH /api/v1/me", s.handleUpdateMe)

	// WebSocket
	mux.HandleFunc("GET /api/v1/ws", s.handleWS)

	// Directory (public)
	mux.HandleFunc("GET /api/v1/categories", s.handleCategoriesTree)
	mux.HandleFunc("GET /api/v1/cities", s.handleCitiesIndex)
	mux.HandleFunc("GET /api/v1/cities/{slug}", s.handleCityPage)
	mux.HandleFunc("GET /api/v1/categories/{slug}", s.handleCategoryPage)
	mux.HandleFunc("GET /api/v1/b/{slug}", s.handleBusinessPage)
	mux.HandleFunc("GET /api/v1/search", s.handleSearch)
	// Public read API (PRD §9.6): same handlers, API-key auth, read-only.
	mux.HandleFunc("GET /api/v2/search", s.apiKeyOnly(s.handleSearch))
	mux.HandleFunc("GET /api/v2/categories", s.apiKeyOnly(s.handleCategoriesTree))
	mux.HandleFunc("GET /api/v2/categories/{slug}", s.apiKeyOnly(s.handleCategoryPage))
	mux.HandleFunc("GET /api/v2/cities/{slug}", s.apiKeyOnly(s.handleCityPage))
	mux.HandleFunc("GET /api/v2/b/{slug}", s.apiKeyOnly(s.handleBusinessPage))
	mux.HandleFunc("GET /api/v2/businesses/{id}/reviews", s.apiKeyOnly(s.handleReviews))
	mux.HandleFunc("GET /api/v2/rates", s.apiKeyOnly(s.handleRates))
	mux.HandleFunc("GET /api/v1/search/suggest", s.handleSuggest)
	mux.HandleFunc("GET /api/v1/compare", s.handleCompare)
	mux.HandleFunc("GET /api/v1/featured", s.handleFeatured)
	mux.HandleFunc("GET /api/v1/rates", s.handleRates)
	mux.HandleFunc("GET /og/b/{slug}", s.handleOGImage)
	mux.HandleFunc("GET /api/v1/sitemap.xml", s.handleSitemap)

	// Businesses (owner)
	mux.HandleFunc("POST /api/v1/businesses", s.handleBusinessCreate)
	mux.HandleFunc("GET /api/v1/businesses", s.handleBusinessList)
	mux.HandleFunc("GET /api/v1/businesses/{id}", s.handleBusinessGet)
	mux.HandleFunc("PATCH /api/v1/businesses/{id}", s.handleBusinessUpdate)
	mux.HandleFunc("POST /api/v1/businesses/{id}/submit", s.handleBusinessSubmit)
	mux.HandleFunc("POST /api/v1/businesses/{id}/resubmit", s.handleBusinessResubmit)
	mux.HandleFunc("POST /api/v1/businesses/{id}/slug-change", s.handleSlugChange)
	mux.HandleFunc("GET /api/v1/businesses/{id}/documents", s.handleDocumentsList)
	mux.HandleFunc("POST /api/v1/businesses/{id}/documents", s.handleDocumentAdd)
	mux.HandleFunc("DELETE /api/v1/businesses/{id}/documents/{docId}", s.handleDocumentRemove)
	mux.HandleFunc("PUT /api/v1/businesses/{id}/storefront", s.handleStorefrontUpdate)
	mux.HandleFunc("POST /api/v1/businesses/{id}/publish", s.handleBusinessPublish)
	mux.HandleFunc("POST /api/v1/businesses/{id}/unpublish", s.handleBusinessUnpublish)
	mux.HandleFunc("POST /api/v1/businesses/{id}/pause", s.handleBusinessPause)
	mux.HandleFunc("POST /api/v1/businesses/{id}/reopen", s.handleBusinessReopen)
	mux.HandleFunc("POST /api/v1/businesses/{id}/close", s.handleBusinessClose)
	mux.HandleFunc("GET /api/v1/businesses/{id}/analytics", s.handleBusinessAnalytics)

	// Products (owner)
	mux.HandleFunc("GET /api/v1/businesses/{id}/products", s.handleProductList)
	mux.HandleFunc("POST /api/v1/businesses/{id}/products", s.handleProductCreate)
	mux.HandleFunc("PATCH /api/v1/products/{productId}", s.handleProductUpdate)
	mux.HandleFunc("PUT /api/v1/products/{productId}/variants", s.handleProductReplaceVariants)
	mux.HandleFunc("POST /api/v1/products/{productId}/publish", s.handleProductPublish)
	mux.HandleFunc("POST /api/v1/products/{productId}/unpublish", s.handleProductUnpublish)
	mux.HandleFunc("DELETE /api/v1/products/{productId}", s.handleProductDelete)
	mux.HandleFunc("POST /api/v1/products/{productId}/duplicate", s.handleProductDuplicate)
	mux.HandleFunc("GET /api/v1/products/{productId}/stock-alert", s.handleStockAlert)
	mux.HandleFunc("PUT /api/v1/products/{productId}/stock-alert", s.handleStockAlert)
	mux.HandleFunc("DELETE /api/v1/products/{productId}/stock-alert", s.handleStockAlert)

	// Media
	mux.HandleFunc("POST /api/v1/media", s.handleMediaUpload)
	mux.HandleFunc("GET /api/v1/media/{id}/file", s.handleMediaServe)
	mux.HandleFunc("GET /api/v1/media/{id}/thumb", s.handleMediaThumb)

	// Admin
	mux.HandleFunc("POST /api/v1/admin/categories", s.adminOnly(s.handleAdminCategoriesCreate))
	mux.HandleFunc("PATCH /api/v1/admin/categories/{id}", s.adminOnly(s.handleAdminCategoriesUpdate))
	mux.HandleFunc("DELETE /api/v1/admin/categories/{id}", s.adminOnly(s.handleAdminCategoriesDelete))
	mux.HandleFunc("POST /api/v1/admin/categories/{id}/merge/{intoId}", s.adminOnly(s.handleAdminCategoriesMerge))
	mux.HandleFunc("GET /api/v1/admin/verify", s.adminOnly(s.handleAdminVerifyQueue))
	mux.HandleFunc("GET /api/v1/admin/verify/{id}", s.adminOnly(s.handleAdminVerifyDetail))
	mux.HandleFunc("POST /api/v1/admin/verify/{id}/decide", s.adminOnly(s.handleAdminVerifyDecide))
	mux.HandleFunc("POST /api/v1/admin/verify/{id}/re-request", s.adminOnly(s.handleAdminDocReRequest))
	mux.HandleFunc("GET /api/v1/admin/verify/{id}/documents/{docId}/file", s.adminOnly(s.handleAdminDocFile))
	mux.HandleFunc("GET /api/v1/admin/reports", s.adminOnly(s.handleAdminReports))
	mux.HandleFunc("POST /api/v1/admin/reports/{reportId}/decide", s.adminOnly(s.handleAdminReportDecide))
	mux.HandleFunc("POST /api/v1/admin/content/{type}/{id}/hide", s.adminOnly(s.handleAdminHide))
	mux.HandleFunc("POST /api/v1/admin/content/{type}/{id}/restore", s.adminOnly(s.handleAdminHide))
	mux.HandleFunc("GET /api/v1/admin/users", s.adminOnly(s.handleAdminUsers))
	mux.HandleFunc("POST /api/v1/admin/users/{userId}/{action}", s.adminOnly(s.handleAdminUserAction))
	mux.HandleFunc("GET /api/v1/admin/curation", s.adminOnly(s.handleAdminCuration))
	mux.HandleFunc("PUT /api/v1/admin/curation", s.adminOnly(s.handleAdminCuration))
	mux.HandleFunc("GET /api/v1/admin/moderation-actions", s.adminOnly(s.handleAdminAudit))
	mux.HandleFunc("GET /api/v1/admin/banned-words", s.adminOnly(s.handleAdminBannedWords))
	mux.HandleFunc("POST /api/v1/admin/banned-words", s.adminOnly(s.handleAdminBannedWords))
	mux.HandleFunc("DELETE /api/v1/admin/banned-words/{word}", s.adminOnly(s.handleAdminBannedWordDelete))
	mux.HandleFunc("GET /api/v1/admin/banned-words/allowlist", s.adminOnly(s.handleAdminAllowlist))
	mux.HandleFunc("PUT /api/v1/admin/banned-words/allowlist", s.adminOnly(s.handleAdminAllowlist))
	mux.HandleFunc("POST /api/v1/admin/businesses/{id}/suspend", s.adminOnly(s.handleAdminBusinessSuspend))
	mux.HandleFunc("POST /api/v1/admin/businesses/{id}/restore", s.adminOnly(s.handleAdminBusinessSuspend))
	mux.HandleFunc("GET /api/v1/admin/settings", s.adminOnly(s.handleAdminSettings))
	mux.HandleFunc("PUT /api/v1/admin/settings", s.adminOnly(s.handleAdminSettings))
	mux.HandleFunc("GET /api/v1/meta", s.handleMeta)

	// Engagement (PRD §5.6)
	mux.HandleFunc("GET /api/v1/likes/{type}/{id}", s.handleLikeState)
	mux.HandleFunc("PUT /api/v1/likes/{type}/{id}", s.handleToggleLike)
	mux.HandleFunc("DELETE /api/v1/likes/{type}/{id}", s.handleToggleLike)
	mux.HandleFunc("GET /api/v1/recommends/{id}", s.handleRecommendState)
	mux.HandleFunc("PUT /api/v1/recommends/{id}", s.handleToggleRecommend)
	mux.HandleFunc("DELETE /api/v1/recommends/{id}", s.handleToggleRecommend)
	mux.HandleFunc("GET /api/v1/me/collections", s.handleCollectionList)
	mux.HandleFunc("POST /api/v1/me/collections", s.handleCollectionCreate)
	mux.HandleFunc("PATCH /api/v1/me/collections/{id}", s.handleCollectionUpdate)
	mux.HandleFunc("DELETE /api/v1/me/collections/{id}", s.handleCollectionDelete)
	mux.HandleFunc("GET /api/v1/me/collections/{id}/items", s.handleCollectionItems)
	mux.HandleFunc("POST /api/v1/me/collections/{id}/items", s.handleCollectionItems)
	mux.HandleFunc("POST /api/v1/me/collections/items", s.handleCollectionItems) // save to default (PRD D3)
	mux.HandleFunc("DELETE /api/v1/me/collections/{id}/items/{itemId}", s.handleCollectionItemRemove)
	mux.HandleFunc("GET /api/v1/saved/{type}/{id}", s.handleSaveState)
	mux.HandleFunc("GET /api/v1/businesses/{id}/comments", s.handleComments)
	mux.HandleFunc("POST /api/v1/businesses/{id}/comments", s.handleComments)
	mux.HandleFunc("PATCH /api/v1/comments/{commentId}", s.handleCommentUpdate)
	mux.HandleFunc("DELETE /api/v1/comments/{commentId}", s.handleCommentDelete)
	mux.HandleFunc("PUT /api/v1/comments/{commentId}/like", s.handleCommentLike)
	mux.HandleFunc("DELETE /api/v1/comments/{commentId}/like", s.handleCommentLike)
	mux.HandleFunc("GET /api/v1/businesses/{id}/reviews", s.handleReviews)
	mux.HandleFunc("POST /api/v1/businesses/{id}/reviews", s.handleReviews)
	mux.HandleFunc("PATCH /api/v1/reviews/{reviewId}", s.handleReviewUpdate)
	mux.HandleFunc("DELETE /api/v1/reviews/{reviewId}", s.handleReviewDelete)
	mux.HandleFunc("POST /api/v1/reviews/{reviewId}/reply", s.handleReviewReply)
	mux.HandleFunc("PUT /api/v1/reviews/{reviewId}/helpful", s.handleReviewHelpful)
	mux.HandleFunc("GET /api/v1/notifications", s.handleNotifications)
	mux.HandleFunc("POST /api/v1/notifications/read", s.handleNotifications)
	mux.HandleFunc("POST /api/v1/reports", s.handleReport)
	mux.HandleFunc("GET /api/v1/businesses/{id}/questions", s.handleQuestions)
	mux.HandleFunc("POST /api/v1/businesses/{id}/questions", s.handleQuestions)
	mux.HandleFunc("POST /api/v1/questions/{questionId}/answers", s.handleAnswer)
	mux.HandleFunc("GET /api/v1/businesses/{id}/follow", s.handleFollow)
	mux.HandleFunc("PUT /api/v1/businesses/{id}/follow", s.handleFollow)
	mux.HandleFunc("DELETE /api/v1/businesses/{id}/follow", s.handleFollow)
	mux.HandleFunc("GET /api/v1/categories/{id}/follow", s.handleCategoryFollow)
	mux.HandleFunc("PUT /api/v1/categories/{id}/follow", s.handleCategoryFollow)
	mux.HandleFunc("DELETE /api/v1/categories/{id}/follow", s.handleCategoryFollow)
	mux.HandleFunc("GET /api/v1/businesses/{id}/updates", s.handleUpdates)
	mux.HandleFunc("POST /api/v1/businesses/{id}/updates", s.handleUpdates)
	mux.HandleFunc("GET /api/v1/me/following-feed", s.handleFollowingFeed)
	mux.HandleFunc("GET /api/v1/u/{username}", s.handlePublicProfile)
	mux.HandleFunc("GET /api/v1/users/search", s.handleUserSearch)
	mux.HandleFunc("POST /api/v1/support/contact", s.handleContact)
	mux.HandleFunc("POST /api/v1/me/appeal", s.handleAppeal)
	mux.HandleFunc("GET /api/v1/admin/appeals", s.adminOnly(s.handleAdminAppeals))
	mux.HandleFunc("POST /api/v1/admin/appeals/{appealId}/decide", s.adminOnly(s.handleAdminAppealDecide))
	mux.HandleFunc("GET /api/v1/admin/anomalies", s.adminOnly(s.handleAdminAnomalies))
	mux.HandleFunc("POST /api/v1/admin/anomalies/{eventId}/resolve", s.adminOnly(s.handleAdminAnomalyResolve))
	mux.HandleFunc("GET /api/v1/businesses/{id}/invites", s.handleInvites)
	mux.HandleFunc("POST /api/v1/businesses/{id}/invites", s.handleInvites)
	mux.HandleFunc("DELETE /api/v1/businesses/{id}/invites/{inviteId}", s.handleInviteRevoke)
	mux.HandleFunc("POST /api/v1/invites/{token}/accept", s.handleInviteAccept)
	mux.HandleFunc("PUT /api/v1/threads/{id}/pin/{messageId}", s.handlePinMessage)
	mux.HandleFunc("DELETE /api/v1/threads/{id}/pin/{messageId}", s.handlePinMessage)
	mux.HandleFunc("GET /api/v1/threads/{id}/pinned", s.handlePinned)
	mux.HandleFunc("GET /api/v1/admin/kpis", s.adminOnly(s.handleAdminKPIs))

	// Trending & leaderboards (PRD §5.6.3)
	mux.HandleFunc("GET /api/v1/leaderboards", s.handleLeaderboard)
	mux.HandleFunc("GET /api/v1/trending", s.handleTrending)
	mux.HandleFunc("GET /api/v1/rising", s.handleRising)

	// Messaging (PRD §5.5)
	mux.HandleFunc("GET /api/v1/threads", s.handleThreads)
	mux.HandleFunc("POST /api/v1/threads", s.handleThreadCreate)
	mux.HandleFunc("GET /api/v1/threads/{id}", s.handleThreadDetail)
	mux.HandleFunc("POST /api/v1/threads/{id}/messages", s.handleMessages)
	mux.HandleFunc("PATCH /api/v1/messages/{messageId}", s.handleMessageEdit)
	mux.HandleFunc("DELETE /api/v1/messages/{messageId}", s.handleMessageDelete)
	mux.HandleFunc("PUT /api/v1/messages/{messageId}/reaction", s.handleMessageReaction)
	mux.HandleFunc("DELETE /api/v1/messages/{messageId}/reaction", s.handleMessageReaction)
	mux.HandleFunc("POST /api/v1/messages/{messageId}/forward", s.handleMessageForward)
	mux.HandleFunc("POST /api/v1/threads/{id}/read", s.handleThreadRead)
	mux.HandleFunc("POST /api/v1/threads/{id}/typing", s.handleTyping)
	mux.HandleFunc("GET /api/v1/threads/{id}/search", s.handleThreadSearch)
	mux.HandleFunc("GET /api/v1/messages/search", s.handleMessageSearch)
	mux.HandleFunc("GET /api/v1/threads/{id}/media", s.handleThreadGallery)
	mux.HandleFunc("GET /api/v1/threads/{id}/export", s.handleThreadExport)
	mux.HandleFunc("POST /api/v1/threads/{id}/close", s.handleThreadClose)
	mux.HandleFunc("PUT /api/v1/threads/{id}/mute", s.handleThreadMute)
	mux.HandleFunc("DELETE /api/v1/threads/{id}/mute", s.handleThreadMute)
	mux.HandleFunc("POST /api/v1/threads/{id}/leave", s.handleThreadLeave)
	mux.HandleFunc("GET /api/v1/blocks", s.handleBlock)
	mux.HandleFunc("POST /api/v1/blocks/{userId}", s.handleBlock)
	mux.HandleFunc("DELETE /api/v1/blocks/{userId}", s.handleBlock)
	mux.HandleFunc("GET /api/v1/businesses/{id}/quick-replies", s.handleQuickReplies)
	mux.HandleFunc("POST /api/v1/businesses/{id}/quick-replies", s.handleQuickReplies)
	mux.HandleFunc("DELETE /api/v1/businesses/{id}/quick-replies/{replyId}", s.handleQuickReplyDelete)
	mux.HandleFunc("POST /api/v1/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("DELETE /api/v1/push/subscribe", s.handlePushUnsubscribe)
}

// adminOnly guards a handler with the admin claim (PRD §5.9.3).
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, found := currentUser(r)
		if !found {
			fail(w, domain.ErrNotAuthenticated)
			return
		}
		if !user.IsAdmin() {
			fail(w, domain.ErrForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) Handler() http.Handler {
	return s.chain(s.mux)
}

// chain builds the middleware stack. Each call wraps the accumulated stack,
// so the FIRST assignment ends up innermost and the request executes in the
// reverse order of these lines:
//
//	recover → accessLog → etag → cors → csrf → auth → rateLimit → admin2FA → routes
//
// auth MUST run before rateLimit/admin2FA: both inspect the authenticated
// user (per-user buckets, 2FA mandate).
func (s *Server) chain(next http.Handler) http.Handler {
	next = s.withAdmin2FA(next)
	next = s.withRateLimit(next)
	next = s.withAuth(next)
	next = s.withCSRF(next)
	next = s.withCORS(next)
	next = s.withETag(next)
	next = s.withAccessLog(next)
	next = s.withRecover(next)
	return next
}

// ---- response helpers (PRD §11.2 envelope) ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any)  { writeJSON(w, http.StatusOK, v) }
func created(w http.ResponseWriter, v any) { writeJSON(w, http.StatusCreated, v) }
func noContent(w http.ResponseWriter)   { w.WriteHeader(http.StatusNoContent) }

func fail(w http.ResponseWriter, err error) {
	de := domain.FromError(err)
	if de.Status >= 500 {
		slog.Error("request failed", "err", err)
	}
	writeJSON(w, de.Status, map[string]any{"error": de})
}

// ---- context keys ----

type ctxKey int

const (
	ctxKeyClaims ctxKey = iota
	ctxKeyUser
)
