package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/util"
)

// ---- likes & recommends ----

func (s *Server) handleLikeState(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	liked, err := s.deps.Engagement.MyLikes(r.Context(), user.ID, r.PathValue("type"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"liked": liked})
}

func (s *Server) handleRecommendState(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	rec, err := s.deps.Engagement.MyRecommends(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"recommended": rec})
}

func (s *Server) handleToggleLike(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	targetType, targetID := r.PathValue("type"), r.PathValue("id")
	if r.Method == http.MethodPut {
		if _, err := s.deps.Engagement.ToggleLike(r.Context(), user.ID, targetType, targetID, true); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"liked": true})
		return
	}
	if _, err := s.deps.Engagement.ToggleLike(r.Context(), user.ID, targetType, targetID, false); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"liked": false})
}

func (s *Server) handleToggleRecommend(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	on := r.Method == http.MethodPut
	if _, err := s.deps.Engagement.ToggleRecommend(r.Context(), user.ID, businessID, on); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"recommended": on})
}

// ---- collections ----

func (s *Server) handleCollectionList(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	list, err := s.deps.Engagement.ListCollections(r.Context(), user.ID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"collections": list})
}

func (s *Server) handleCollectionCreate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := s.deps.Engagement.CreateCollection(r.Context(), user.ID, in.Name)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"collection": c})
}

func (s *Server) handleCollectionUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Name     *string `json:"name"`
		IsPublic *bool   `json:"is_public"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := s.deps.Engagement.UpdateCollection(r.Context(), user.ID, r.PathValue("id"), in.Name, in.IsPublic)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"collection": c})
}

func (s *Server) handleCollectionDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Engagement.DeleteCollection(r.Context(), user.ID, r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleCollectionItems(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		items, err := s.deps.Engagement.ListItems(r.Context(), user.ID, r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"items": items})
		return
	}
	var in struct {
		TargetType   string  `json:"target_type"`
		TargetID     string  `json:"target_id"`
		Note         *string `json:"note"`
		CollectionID string  `json:"collection_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	collectionID := in.CollectionID
	if collectionID == "" {
		collectionID = r.PathValue("id")
	}
	var note string
	if in.Note != nil {
		note = *in.Note
	}
	if err := s.deps.Engagement.SaveTo(r.Context(), user.ID, collectionID, in.TargetType, in.TargetID, note); err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"saved": true})
}

func (s *Server) handleCollectionItemRemove(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Engagement.RemoveItem(r.Context(), user.ID, r.PathValue("id"), r.PathValue("itemId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleSaveState(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	ids, err := s.deps.Engagement.InCollections(r.Context(), user.ID, r.PathValue("type"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"collection_ids": ids})
}

// ---- comments ----

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 50)
		offset := parsePositiveInt(r.URL.Query().Get("offset"), 0)
		list, err := s.deps.Engagement.ListComments(r.Context(), r.PathValue("id"), limit, offset)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"comments": list})
		return
	}
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Text     string `json:"text"`
		ParentID string `json:"parent_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := s.deps.Engagement.CreateComment(r.Context(), user.ID, r.PathValue("id"), in.ParentID, in.Text)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"comment": c})
}

func (s *Server) handleCommentUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := s.deps.Engagement.UpdateComment(r.Context(), user.ID, r.PathValue("commentId"), in.Text)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"comment": c})
}

func (s *Server) handleCommentDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Engagement.DeleteComment(r.Context(), user.ID, r.PathValue("commentId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleCommentLike(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Engagement.ToggleCommentLike(r.Context(), user.ID, r.PathValue("commentId"), r.Method == http.MethodPut); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"liked": r.Method == http.MethodPut})
}

// ---- reviews ----

func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
		offset := parsePositiveInt(r.URL.Query().Get("offset"), 0)
		sort := r.URL.Query().Get("sort")
		if sort == "" {
			sort = "newest"
		}
		var productID *string
		if pid := r.URL.Query().Get("product_id"); pid != "" {
			productID = &pid
		}
		// my_vote is included when a session is present so the UI can
		// toggle votes off (PRD §5.6.1).
		var viewerID *string
		if user, found := currentUser(r); found {
			viewerID = &user.ID
		}
		list, err := s.deps.Engagement.ListReviews(r.Context(), businessID, productID, sort, limit, offset, viewerID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"reviews": list})
		return
	}
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Rating    int      `json:"rating"`
		Text      string   `json:"text"`
		ProductID *string  `json:"product_id"`
		ImageIDs  []string `json:"image_ids"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	rw, err := s.deps.Engagement.CreateReview(r.Context(), user.ID, businessID, in.ProductID, in.Rating, in.Text, in.ImageIDs)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"review": rw})
}

func (s *Server) handleReviewUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Rating   int      `json:"rating"`
		Text     string   `json:"text"`
		ImageIDs []string `json:"image_ids"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	rw, err := s.deps.Engagement.UpdateReview(r.Context(), user.ID, r.PathValue("reviewId"), in.Rating, in.Text, in.ImageIDs)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"review": rw})
}

func (s *Server) handleReviewDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Engagement.DeleteReview(r.Context(), user.ID, r.PathValue("reviewId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleReviewReply(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	// POST creates, PATCH edits (stamps reply_edited_at), DELETE removes —
	// PRD §5.6.2 owner reply lifecycle.
	if r.Method == http.MethodDelete {
		if err := s.deps.Engagement.DeleteReviewReply(r.Context(), user.ID, r.PathValue("reviewId")); err != nil {
			fail(w, err)
			return
		}
		noContent(w)
		return
	}
	var in struct {
		Reply string `json:"reply"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	var (
		rw  *domain.Review
		err error
	)
	if r.Method == http.MethodPatch {
		rw, err = s.deps.Engagement.EditReviewReply(r.Context(), user.ID, r.PathValue("reviewId"), in.Reply)
	} else {
		rw, err = s.deps.Engagement.ReplyToReview(r.Context(), user.ID, r.PathValue("reviewId"), in.Reply)
	}
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"review": rw})
}

func (s *Server) handleReviewHelpful(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Vote int `json:"vote"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Engagement.ToggleHelpful(r.Context(), user.ID, r.PathValue("reviewId"), in.Vote); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"voted": in.Vote})
}

// ---- notifications ----

// MyReviews lists the signed-in user's reviews (PRD §5.6.2).
func (s *Server) handleMyReviews(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 30)
	offset := parsePositiveInt(r.URL.Query().Get("offset"), 0)
	list, err := s.deps.Engagement.MyReviews(r.Context(), user.ID, limit, offset)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"reviews": list})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 30)
		offset := parsePositiveInt(r.URL.Query().Get("offset"), 0)
		ntype := r.URL.Query().Get("type")
		list, unread, err := s.deps.Engagement.ListNotifications(r.Context(), user.ID, ntype, limit, offset)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"notifications": list, "unread": unread})
		return
	}
	var in struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.deps.Engagement.MarkRead(r.Context(), user.ID, in.IDs, in.All); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

// ---- leaderboards & trending ----

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("window")
	if period != "24h" && period != "7d" && period != "30d" {
		period = "24h"
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "global"
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 50)
	entries, err := s.deps.Trending.Leaderboard(r.Context(), period, scope, limit)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"entries": entries, "period": period, "scope": scope})
}

func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
	entries, err := s.deps.Trending.Leaderboard(r.Context(), "24h", "global", 10)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"entries": entries})
}

func (s *Server) handleRising(w http.ResponseWriter, r *http.Request) {
	entries, err := s.deps.Trending.Leaderboard(r.Context(), "24h", "rising", 10)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"entries": entries})
}

// ---- reports (PRD §5.5.4) ----

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Reason     string `json:"reason"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.TargetType == "" || in.TargetID == "" || in.Reason == "" {
		fail(w, domain.ErrValidation.WithField("_", "target_type, target_id, and reason are required."))
		return
	}
	_, err := s.deps.Repos.Exec(r.Context(), `
		INSERT INTO reports (id, reporter_id, target_type, target_id, reason)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (reporter_id, target_type, target_id) WHERE status='open' DO NOTHING`,
		util.NewUUID(), user.ID, in.TargetType, in.TargetID, in.Reason)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"reported": true})
}
