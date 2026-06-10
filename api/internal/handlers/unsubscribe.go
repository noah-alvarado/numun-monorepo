package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	"github.com/numun/numun/api/internal/store"
)

// UnsubscribeRoutes handles the GET/POST List-Unsubscribe one-click handler
// referenced in EMAIL.md §2.2. Token validation is stateless (HMAC); hitting
// the URL flips announcementsOptIn = false on the resolved User row.
type UnsubscribeRoutes struct {
	Store  *store.Client
	Cfg    email.Config
	Logger *slog.Logger
}

// Register binds /v1/email/unsubscribe (GET + POST) on the given mux.
func (u *UnsubscribeRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/email/unsubscribe", u.handle)
	mux.HandleFunc("POST /v1/email/unsubscribe", u.handle)
}

func (u *UnsubscribeRoutes) handle(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	payload, err := email.VerifyUnsubscribeToken(u.Cfg.UnsubscribeSecret, token)
	if err != nil {
		u.log().Warn("unsubscribe: token", "err", err)
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	user, err := u.Store.GetUser(r.Context(), payload.UserID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if user.AnnouncementsOptIn {
		off := false
		patch := store.UpdateUserPatch{AnnouncementsOptIn: &off, UpdatedBy: user.ID}
		if _, err := u.Store.UpdateUser(r.Context(), user.ID, user.Version, patch); err != nil {
			u.log().Warn("unsubscribe: update", "err", err)
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		// Forensic event so an admin can see when the click happened.
		_ = u.Store.RecordAuthEvent(r.Context(), domain.AuthAuditEvent{
			UserID:      user.ID,
			ActorUserID: user.ID,
			Kind:        "announcements_unsubscribed",
		})
	}
	// One-Click POST returns 204; GET shows a confirmation page.
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "You've been unsubscribed from NUMUN announcements.",
	})
}

func (u *UnsubscribeRoutes) log() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}
