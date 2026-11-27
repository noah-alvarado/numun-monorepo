package handlers

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	"github.com/numun/numun/api/internal/store"
)

// UnsubscribeRoutes serves the announcement unsubscribe link (EMAIL.md §2.2).
// Tokens are stateless HMACs naming the user.
//
//   - GET shows a confirmation page with an Unsubscribe button. It changes
//     nothing, so mail scanners that prefetch links can't opt people out.
//   - POST from that button flips announcementsOptIn off and shows a done page.
//   - POST with the RFC 8058 body (List-Unsubscribe=One-Click), sent by mail
//     clients' own unsubscribe button, does the same and returns 204.
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
	oneClick := r.Method == http.MethodPost && r.FormValue("List-Unsubscribe") == "One-Click"
	if token == "" {
		u.fail(w, oneClick, http.StatusBadRequest, "This unsubscribe link is incomplete. Use the link from the email.")
		return
	}
	payload, err := email.VerifyUnsubscribeToken(u.Cfg.UnsubscribeSecret, token)
	if err != nil {
		u.log().Warn("unsubscribe: token", "err", err)
		u.fail(w, oneClick, http.StatusForbidden, "This unsubscribe link isn't valid. Use the link from the email, or turn off announcements in your portal profile.")
		return
	}
	user, err := u.Store.GetUser(r.Context(), payload.UserID)
	if err != nil {
		u.fail(w, oneClick, http.StatusNotFound, "We couldn't find the account this link belongs to.")
		return
	}
	if r.Method == http.MethodGet {
		u.page(w, http.StatusOK, unsubscribePage{
			Title:   "Unsubscribe from NUMUN announcements?",
			Message: "You'll still get emails about your own delegation, like approvals and payments.",
			Email:   user.Email,
			Action:  r.URL.RequestURI(),
		})
		return
	}
	if user.AnnouncementsOptIn {
		off := false
		patch := store.UpdateUserPatch{AnnouncementsOptIn: &off, UpdatedBy: user.ID}
		if _, err := u.Store.UpdateUser(r.Context(), user.ID, user.Version, patch); err != nil {
			u.log().Warn("unsubscribe: update", "err", err)
			u.fail(w, oneClick, http.StatusInternalServerError, "Something went wrong. Try again in a minute.")
			return
		}
		// Forensic event so an admin can see when the click happened.
		_ = u.Store.RecordAuthEvent(r.Context(), domain.AuthAuditEvent{
			UserID:      user.ID,
			ActorUserID: user.ID,
			Kind:        "announcements_unsubscribed",
		})
	}
	if oneClick {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	u.page(w, http.StatusOK, unsubscribePage{
		Title:      "You've been unsubscribed",
		Message:    "You won't get NUMUN announcements any more. You can turn them back on in your portal profile.",
		ProfileURL: strings.TrimRight(u.Cfg.PortalBaseURL, "/") + "/profile",
	})
}

func (u *UnsubscribeRoutes) fail(w http.ResponseWriter, oneClick bool, status int, msg string) {
	if oneClick {
		http.Error(w, msg, status)
		return
	}
	u.page(w, status, unsubscribePage{Title: "Couldn't unsubscribe", Message: msg})
}

type unsubscribePage struct {
	Title, Message, Email, Action, ProfileURL string
}

var unsubscribeTmpl = template.Must(template.New("unsubscribe").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · NUMUN</title>
<style>body{font-family:system-ui,sans-serif;background:#e4e0ee;color:#1d1031;margin:0}main{max-width:32rem;margin:4rem auto;background:#fff;border:1px solid #c7bde0;border-radius:6px;padding:2rem}h1{color:#4e2a84;font-size:1.4rem;margin-top:0}button{background:#4e2a84;color:#fff;border:0;border-radius:4px;padding:.5rem 1rem;font-size:1rem;cursor:pointer}a{color:#4e2a84}</style>
</head><body><main>
<h1>{{.Title}}</h1>
{{with .Email}}<p>{{.}}</p>{{end}}
<p>{{.Message}}</p>
{{with .Action}}<form method="post" action="{{.}}"><button type="submit">Unsubscribe</button></form>{{end}}
{{with .ProfileURL}}<p><a href="{{.}}">Open your profile</a></p>{{end}}
</main></body></html>`))

func (u *UnsubscribeRoutes) page(w http.ResponseWriter, status int, p unsubscribePage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := unsubscribeTmpl.Execute(w, p); err != nil {
		u.log().Warn("unsubscribe: render", "err", err)
	}
}

func (u *UnsubscribeRoutes) log() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}
