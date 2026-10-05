package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
)

// ---- Q&A (B5) ----

func (s *Server) handleQuestions(w http.ResponseWriter, r *http.Request) {
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
		offset := parseOffset(r.URL.Query().Get("offset"), 0)
		list, err := s.deps.Community.Questions(r.Context(), businessID, limit, offset)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"questions": list})
		return
	}
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
	q, err := s.deps.Community.Ask(r.Context(), user.ID, businessID, in.Text)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"question": q})
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
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
	a, err := s.deps.Community.Answer(r.Context(), user.ID, r.PathValue("questionId"), in.Text)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"answer": a})
}

// ---- follows (B6) ----

func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		following, err := s.deps.Community.Following(r.Context(), user.ID, businessID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"following": following})
		return
	}
	on := r.Method == http.MethodPut
	if err := s.deps.Community.Follow(r.Context(), user.ID, businessID, on); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"following": on})
}

// ---- category follows (B4) ----

func (s *Server) handleCategoryFollow(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	categoryID := r.PathValue("id")
	if r.Method == http.MethodGet {
		following, err := s.deps.Community.FollowingCategory(r.Context(), user.ID, categoryID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"following": following})
		return
	}
	on := r.Method == http.MethodPut
	if err := s.deps.Community.FollowCategory(r.Context(), user.ID, categoryID, on); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"following": on})
}

// ---- owner announcements (B6) ----

func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	businessID := r.PathValue("id")
	if r.Method == http.MethodGet {
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
		list, err := s.deps.Community.Updates(r.Context(), businessID, limit)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"updates": list})
		return
	}
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	u, err := s.deps.Community.PostUpdate(r.Context(), user.ID, businessID, in.Title, in.Body)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"update": u})
}

// ---- following feed (B6) ----

func (s *Server) handleFollowingFeed(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
	list, err := s.deps.Community.FollowingFeed(r.Context(), user.ID, limit)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"updates": list})
}
