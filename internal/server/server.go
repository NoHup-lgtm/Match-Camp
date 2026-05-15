package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"matchcamp/internal/apperror"
	"matchcamp/internal/auth"
	db "matchcamp/internal/database/db"
	"matchcamp/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

const sessionTTL = 30 * 24 * time.Hour
const oauthStateCookieName = "matchcamp_oauth_state"
const profilePhotoFormField = "photo"

const (
	wsPingInterval = 30 * time.Second
	wsPongWait     = 60 * time.Second
	wsWriteWait    = 10 * time.Second
)

type Config struct {
	DB                   *pgxpool.Pool
	Redis                *redis.Client
	Storage              storage.ObjectStore
	Log                  *slog.Logger
	SessionCookieName    string
	SessionCookieSecure  bool
	AllowedEmailDomains  []string
	AllowedOrigins       []string
	GoogleClientID       string
	GoogleClientSecret   string
	GoogleRedirectURL    string
	PublicBaseURL        string
	UploadDir            string
	MaxProfilePhotoBytes int64
}

type Server struct {
	db                   *pgxpool.Pool
	queries              *db.Queries
	redis                *redis.Client
	storage              storage.ObjectStore
	log                  *slog.Logger
	sessionCookieName    string
	sessionCookieSecure  bool
	allowedDomains       map[string]struct{}
	allowedOrigins       map[string]struct{}
	googleClientID       string
	googleClientSecret   string
	googleRedirectURL    string
	publicBaseURL        string
	uploadDir            string
	maxProfilePhotoBytes int64
	upgrader             websocket.Upgrader
	registerLimiter      *keyedLimiter
	loginLimiter         *keyedLimiter
	swipeLimiter         *keyedLimiter
	messageLimiter       *keyedLimiter
}

type userContextKey struct{}

type currentUser struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"display_name"`
}

func New(cfg Config) *Server {
	domains := make(map[string]struct{}, len(cfg.AllowedEmailDomains))
	for _, domain := range cfg.AllowedEmailDomains {
		domains[strings.ToLower(domain)] = struct{}{}
	}
	origins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		origins[strings.ToLower(o)] = struct{}{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Server{
		db:                   cfg.DB,
		queries:              db.New(cfg.DB),
		redis:                cfg.Redis,
		storage:              cfg.Storage,
		log:                  cfg.Log,
		sessionCookieName:    cfg.SessionCookieName,
		sessionCookieSecure:  cfg.SessionCookieSecure,
		allowedDomains:       domains,
		allowedOrigins:       origins,
		googleClientID:       cfg.GoogleClientID,
		googleClientSecret:   cfg.GoogleClientSecret,
		googleRedirectURL:    cfg.GoogleRedirectURL,
		publicBaseURL:        strings.TrimRight(cfg.PublicBaseURL, "/"),
		uploadDir:            cfg.UploadDir,
		maxProfilePhotoBytes: cfg.MaxProfilePhotoBytes,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		// IP-based: 5 req/min burst 10 for register; 10 req/min burst 20 for login
		registerLimiter: newKeyedLimiter(rate.Every(12*time.Second), 10),
		loginLimiter:    newKeyedLimiter(rate.Every(6*time.Second), 20),
		// User-based: 60 req/min for swipes; 30 req/min burst 60 for messages
		swipeLimiter:   newKeyedLimiter(rate.Every(time.Second), 60),
		messageLimiter: newKeyedLimiter(rate.Every(2*time.Second), 60),
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(s.cors)

	r.Get("/health", s.health)
	r.Get("/openapi.yaml", s.openapi)
	r.Get("/docs", s.docs)
	r.Get("/docs/", s.docs)
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.uploadDir))))

	r.Route("/v1", func(r chi.Router) {
		r.Post("/auth/register", s.register)
		r.Post("/auth/login", s.login)
		r.Get("/auth/google/start", s.googleStart)
		r.Get("/auth/google/callback", s.googleCallback)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.logout)
			r.Get("/me", s.me)
			r.Put("/profile", s.upsertProfile)
			r.Patch("/profile/visibility", s.updateVisibility)
			r.Put("/profile/preferences", s.upsertPreferences)
			r.Get("/profile/photos", s.listMyProfilePhotos)
			r.Put("/profile/photos/{position}", s.uploadProfilePhoto)
			r.Delete("/profile/photos/{position}", s.deleteProfilePhoto)
			r.Get("/discovery", s.discovery)
			r.Post("/swipes", s.swipe)
			r.Get("/matches", s.matches)
			r.Get("/users/{id}", s.publicProfile)
			r.Post("/users/{id}/block", s.blockUser)
			r.Delete("/users/{id}/block", s.unblockUser)
			r.Get("/conversations", s.conversations)
			r.Get("/conversations/{id}/messages", s.messages)
			r.Post("/conversations/{id}/messages", s.createMessageHTTP)
			r.Get("/ws", s.websocket)
		})
	})

	return r
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := len(s.allowedOrigins) == 0
			if !allowed {
				_, allowed = s.allowedOrigins[strings.ToLower(origin)]
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Add("Vary", "Origin")
			}
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, r, "database_unavailable")
		return
	}
	if err := s.redis.Ping(ctx).Err(); err != nil {
		writeError(w, r, "redis_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "docs/openapi.yaml")
}

func (s *Server) docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="pt-BR">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Matchcamp API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({ url: "/openapi.yaml", dom_id: "#swagger-ui" });
  </script>
</body>
</html>`))
}

type registerRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.registerLimiter.allow(clientIP(r)) {
		writeError(w, r, "rate_limit_exceeded")
		return
	}
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Email == "" || req.DisplayName == "" || len(req.Password) < auth.PasswordMinLen {
		writeError(w, r, "invalid_register_payload")
		return
	}
	if !s.emailAllowed(req.Email) {
		writeError(w, r, "email_domain_not_allowed")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, r, "password_hash_failed")
		return
	}
	created, err := s.queries.CreatePasswordUser(r.Context(), db.CreatePasswordUserParams{
		Email:       req.Email,
		DisplayName: req.DisplayName,
		PasswordHash: pgtype.Text{
			String: hash,
			Valid:  true,
		},
	})
	if isUniqueViolation(err) {
		writeError(w, r, "email_already_registered")
		return
	}
	if err != nil {
		writeError(w, r, "register_failed")
		return
	}
	user := currentUser{ID: created.ID, Email: created.Email, Name: created.DisplayName}
	if err := s.createSession(w, r, user.ID); err != nil {
		writeError(w, r, "session_create_failed")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.allow(clientIP(r)) {
		writeError(w, r, "rate_limit_exceeded")
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	row, err := s.queries.GetUserForLogin(r.Context(), req.Email)
	if errors.Is(err, pgx.ErrNoRows) || !row.PasswordHash.Valid || !auth.VerifyPassword(row.PasswordHash.String, req.Password) {
		writeError(w, r, "invalid_credentials")
		return
	}
	if err != nil {
		writeError(w, r, "login_failed")
		return
	}
	user := currentUser{ID: row.ID, Email: row.Email, Name: row.DisplayName}
	if err := s.createSession(w, r, user.ID); err != nil {
		writeError(w, r, "session_create_failed")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	if s.googleClientID == "" || s.googleRedirectURL == "" {
		writeError(w, r, "google_oauth_not_configured")
		return
	}
	state, _, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, r, "oauth_state_failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure,
	})
	v := url.Values{}
	v.Set("client_id", s.googleClientID)
	v.Set("redirect_uri", s.googleRedirectURL)
	v.Set("response_type", "code")
	v.Set("scope", "openid email profile")
	v.Set("access_type", "offline")
	v.Set("state", state)
	http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+v.Encode(), http.StatusFound)
}

func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		writeError(w, r, "invalid_oauth_state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, r, "missing_oauth_code")
		return
	}
	profile, err := s.fetchGoogleProfile(r.Context(), code)
	if err != nil {
		s.log.Warn("google oauth failed", "error", err)
		writeError(w, r, "google_oauth_failed")
		return
	}
	if !profile.EmailVerified {
		writeError(w, r, "google_email_not_verified")
		return
	}
	if !s.emailAllowed(profile.Email) {
		writeError(w, r, "email_domain_not_allowed")
		return
	}
	user, err := s.upsertGoogleUser(r.Context(), profile)
	if err != nil {
		writeError(w, r, "google_user_save_failed")
		return
	}
	if err := s.createSession(w, r, user.ID); err != nil {
		writeError(w, r, "session_create_failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure,
	})
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(s.sessionCookieName)
	if err == nil {
		_ = s.queries.DeleteSessionByTokenHash(r.Context(), auth.HashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	row, err := s.queries.GetMyProfile(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, "profile_fetch_failed")
		return
	}
	photos, err := s.queries.ListProfilePhotos(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, "profile_photos_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           row.ID,
		"email":        row.Email,
		"display_name": row.DisplayName,
		"bio":          row.Bio,
		"course":       row.Course,
		"campus":       row.Campus,
		"age":          calcAge(row.BirthDate),
		"visible":      row.Visible,
		"photos":       profilePhotoResponses(photos),
	})
}

type profileRequest struct {
	Bio       string `json:"bio"`
	Course    string `json:"course"`
	Campus    string `json:"campus"`
	BirthDate string `json:"birth_date"`
}

func (s *Server) upsertProfile(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var req profileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Bio = strings.TrimSpace(req.Bio)
	req.Course = strings.TrimSpace(req.Course)
	req.Campus = strings.TrimSpace(req.Campus)
	if len(req.Bio) > 500 || len(req.Course) > 120 || len(req.Campus) > 120 {
		writeError(w, r, "profile_too_large")
		return
	}
	var birthDate pgtype.Date
	if strings.TrimSpace(req.BirthDate) != "" {
		parsed, err := time.Parse("2006-01-02", req.BirthDate)
		if err != nil {
			writeError(w, r, "invalid_birth_date")
			return
		}
		birthDate = pgtype.Date{Time: parsed, Valid: true}
	}
	err := s.queries.UpsertProfile(r.Context(), db.UpsertProfileParams{
		UserID:    user.ID,
		Bio:       req.Bio,
		Course:    req.Course,
		Campus:    req.Campus,
		BirthDate: birthDate,
	})
	if err != nil {
		writeError(w, r, "profile_save_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

type visibilityRequest struct {
	Visible bool `json:"visible"`
}

type profilePhotoResponsePayload struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	URL       string    `json:"url"`
	Position  int32     `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) updateVisibility(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var req visibilityRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rowsAffected, err := s.queries.UpdateProfileVisibility(r.Context(), db.UpdateProfileVisibilityParams{
		UserID:  user.ID,
		Visible: req.Visible,
	})
	if err != nil {
		writeError(w, r, "visibility_update_failed")
		return
	}
	if rowsAffected == 0 {
		writeError(w, r, "profile_incomplete")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"visible": req.Visible})
}

func (s *Server) upsertPreferences(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var req struct {
		InterestedIn string `json:"interested_in"`
		MinAge       int32  `json:"min_age"`
		MaxAge       int32  `json:"max_age"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	allowed := map[string]bool{"everyone": true, "men": true, "women": true}
	if !allowed[req.InterestedIn] || req.MinAge < 18 || req.MaxAge > 99 || req.MinAge > req.MaxAge {
		writeError(w, r, "invalid_preferences_payload")
		return
	}
	if err := s.queries.UpsertPreferences(r.Context(), db.UpsertPreferencesParams{
		UserID:       user.ID,
		InterestedIn: req.InterestedIn,
		MinAge:       req.MinAge,
		MaxAge:       req.MaxAge,
	}); err != nil {
		writeError(w, r, "preferences_save_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interested_in": req.InterestedIn,
		"min_age":       req.MinAge,
		"max_age":       req.MaxAge,
	})
}

func (s *Server) listMyProfilePhotos(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	photos, err := s.queries.ListProfilePhotos(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, "profile_photos_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, profilePhotoResponses(photos))
}

func (s *Server) uploadProfilePhoto(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	position, ok := parsePhotoPosition(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxProfilePhotoBytes+1024)
	if err := r.ParseMultipartForm(s.maxProfilePhotoBytes + 1024); err != nil {
		writeError(w, r, "invalid_multipart_photo")
		return
	}
	file, header, err := r.FormFile(profilePhotoFormField)
	if err != nil {
		writeError(w, r, "missing_photo_file")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, s.maxProfilePhotoBytes+1))
	if err != nil {
		writeError(w, r, "photo_read_failed")
		return
	}
	if int64(len(data)) > s.maxProfilePhotoBytes {
		writeError(w, r, "profile_photo_too_large")
		return
	}
	contentType, ext, ok := storage.DetectImageExtension(data)
	if !ok {
		writeError(w, r, "unsupported_profile_photo_type")
		return
	}
	if header.Size > s.maxProfilePhotoBytes {
		writeError(w, r, "profile_photo_too_large")
		return
	}

	oldPhoto, oldErr := s.queries.GetProfilePhotoByPosition(r.Context(), db.GetProfilePhotoByPositionParams{
		UserID:   user.ID,
		Position: int32(position),
	})

	key, err := storage.ProfilePhotoKey(user.ID, position, ext)
	if err != nil {
		writeError(w, r, "profile_photo_name_failed")
		return
	}
	photoURL, err := s.storage.Put(r.Context(), key, contentType, data)
	if err != nil {
		s.log.Warn("profile photo upload failed", "error", err)
		writeError(w, r, "profile_photo_upload_failed")
		return
	}

	photo, err := s.queries.UpsertProfilePhoto(r.Context(), db.UpsertProfilePhotoParams{
		UserID:   user.ID,
		Url:      photoURL,
		Position: int32(position),
	})
	if err != nil {
		_ = s.storage.Delete(r.Context(), key)
		writeError(w, r, "profile_photo_save_failed")
		return
	}
	if oldErr == nil {
		s.removeProfilePhotoObject(r.Context(), oldPhoto.Url)
	}
	writeJSON(w, http.StatusOK, profilePhotoResponse(photo))
}

func (s *Server) deleteProfilePhoto(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	position, ok := parsePhotoPosition(w, r)
	if !ok {
		return
	}
	photo, err := s.queries.DeleteProfilePhotoByPosition(r.Context(), db.DeleteProfilePhotoByPositionParams{
		UserID:   user.ID,
		Position: int32(position),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, "profile_photo_not_found")
		return
	}
	if err != nil {
		writeError(w, r, "profile_photo_delete_failed")
		return
	}
	s.removeProfilePhotoObject(r.Context(), photo.Url)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) publicProfile(w http.ResponseWriter, r *http.Request) {
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, "invalid_uuid")
		return
	}
	profile, err := s.queries.GetPublicProfile(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, "user_not_found")
			return
		}
		writeError(w, r, "profile_fetch_failed")
		return
	}
	photos, err := s.queries.ListProfilePhotos(r.Context(), targetID)
	if err != nil {
		writeError(w, r, "profile_photos_list_failed")
		return
	}
	type photo struct {
		ID       uuid.UUID `json:"id"`
		URL      string    `json:"url"`
		Position int32     `json:"position"`
	}
	photoList := make([]photo, 0, len(photos))
	for _, ph := range photos {
		photoList = append(photoList, photo{ID: ph.ID, URL: ph.Url, Position: ph.Position})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           profile.ID,
		"display_name": profile.DisplayName,
		"age":          calcAge(profile.BirthDate),
		"bio":          profile.Bio,
		"course":       profile.Course,
		"campus":       profile.Campus,
		"photos":       photoList,
	})
}

func (s *Server) blockUser(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, "invalid_uuid")
		return
	}
	if targetID == user.ID {
		writeError(w, r, "cannot_block_self")
		return
	}
	if err := s.queries.BlockUser(r.Context(), db.BlockUserParams{
		BlockerID: user.ID,
		BlockedID: targetID,
	}); err != nil {
		writeError(w, r, "block_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unblockUser(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, "invalid_uuid")
		return
	}
	if err := s.queries.UnblockUser(r.Context(), db.UnblockUserParams{
		BlockerID: user.ID,
		BlockedID: targetID,
	}); err != nil {
		writeError(w, r, "unblock_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) discovery(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	limit, offset := parsePagination(r, 25, 100)
	rows, err := s.queries.ListDiscoveryProfiles(r.Context(), db.ListDiscoveryProfilesParams{
		UserID: user.ID,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		writeError(w, r, "discovery_failed")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		photos, err := s.queries.ListProfilePhotos(r.Context(), row.ID)
		if err != nil {
			writeError(w, r, "discovery_photos_failed")
			return
		}
		items = append(items, map[string]any{
			"id":           row.ID,
			"display_name": row.DisplayName,
			"age":          calcAge(row.BirthDate),
			"bio":          row.Bio,
			"course":       row.Course,
			"campus":       row.Campus,
			"photos":       profilePhotoResponses(photos),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

type swipeRequest struct {
	TargetUserID uuid.UUID `json:"target_user_id"`
	Action       string    `json:"action"`
}

func (s *Server) swipe(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	if !s.swipeLimiter.allow(user.ID.String()) {
		writeError(w, r, "rate_limit_exceeded")
		return
	}
	var req swipeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TargetUserID == uuid.Nil || req.TargetUserID == user.ID || (req.Action != "like" && req.Action != "pass") {
		writeError(w, r, "invalid_swipe")
		return
	}
	matchID, conversationID, matched, err := s.recordSwipe(r.Context(), user.ID, req.TargetUserID, req.Action)
	if isUniqueViolation(err) {
		writeError(w, r, "swipe_already_exists")
		return
	}
	if err != nil {
		writeError(w, r, "swipe_failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"matched":         matched,
		"match_id":        matchID,
		"conversation_id": conversationID,
	})
}

func (s *Server) matches(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	rows, err := s.queries.ListMatchesForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, "matches_failed")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		partner := map[string]any{
			"id":           row.PartnerID,
			"display_name": row.PartnerDisplayName,
			"age":          calcAge(row.PartnerBirthDate),
			"photo_url":    nilIfEmpty(row.PartnerPhotoUrl),
		}
		var lastMsg any
		if row.LastMessageAt.Valid {
			lastMsg = map[string]any{
				"text":       row.LastMessageBody,
				"created_at": timestamptz(row.LastMessageAt),
				"from_me":    row.LastMessageSenderID == user.ID,
			}
		}
		out = append(out, map[string]any{
			"match_id":        row.MatchID,
			"conversation_id": row.ConversationID,
			"created_at":      timestamptz(row.CreatedAt),
			"partner":         partner,
			"last_message":    lastMsg,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	rows, err := s.queries.ListConversationsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, "conversations_failed")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		partner := map[string]any{
			"id":           row.PartnerID,
			"display_name": row.PartnerDisplayName,
			"age":          calcAge(row.PartnerBirthDate),
			"photo_url":    nilIfEmpty(row.PartnerPhotoUrl),
			"online":       s.isOnline(r.Context(), row.PartnerID),
		}
		var lastMsg any
		if row.LastMessageAt.Valid {
			lastMsg = map[string]any{
				"text":       row.LastMessageBody,
				"created_at": timestamptz(row.LastMessageAt),
				"from_me":    row.LastMessageSenderID == user.ID,
			}
		}
		out = append(out, map[string]any{
			"conversation_id": row.ConversationID,
			"match_id":        row.MatchID,
			"created_at":      timestamptz(row.CreatedAt),
			"partner":         partner,
			"last_message":    lastMsg,
			"unread_count":    row.UnreadCount,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	conversationID, ok := parseRouteUUID(w, r, "id")
	if !ok {
		return
	}
	if !s.isConversationMember(r.Context(), conversationID, user.ID) {
		writeError(w, r, "not_conversation_member")
		return
	}
	limit, beforeCreatedAt := parseMessagePagination(r)
	rows, err := s.queries.ListMessagesPaginated(r.Context(), db.ListMessagesPaginatedParams{
		ConversationID:  conversationID,
		BeforeCreatedAt: beforeCreatedAt,
		LimitCount:      int32(limit),
	})
	if err != nil {
		writeError(w, r, "messages_failed")
		return
	}
	// Marcar mensagens do parceiro como lidas.
	_ = s.queries.MarkMessagesRead(r.Context(), db.MarkMessagesReadParams{
		ConversationID: conversationID,
		SenderUserID:   user.ID,
	})
	// Query retorna DESC; reverter para exibição cronológica.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":              row.ID,
			"conversation_id": row.ConversationID,
			"sender_user_id":  row.SenderUserID,
			"text":            row.Body,
			"is_read":         row.IsRead,
			"created_at":      timestamptz(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createMessageHTTP(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	if !s.messageLimiter.allow(user.ID.String()) {
		writeError(w, r, "rate_limit_exceeded")
		return
	}
	conversationID, ok := parseRouteUUID(w, r, "id")
	if !ok {
		return
	}
	req, ok := decodeChatPayload(w, r)
	if !ok {
		return
	}
	if req.ConversationID != conversationID {
		writeError(w, r, "conversation_id_mismatch")
		return
	}
	req.ConversationID = conversationID
	msg, err := s.createMessage(r.Context(), user.ID, req)
	if err != nil {
		writeChatError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Serializa todas as escritas para evitar panic com goroutines concorrentes.
	var writeMu sync.Mutex
	wsWrite := func(msgType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		return conn.WriteMessage(msgType, data)
	}
	wsWriteJSON := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return wsWrite(websocket.TextMessage, data)
	}

	presenceKey := "presence:" + user.ID.String()
	_ = s.redis.Set(ctx, presenceKey, "online", 45*time.Second).Err()
	defer s.redis.Del(context.Background(), presenceKey)

	pubsub := s.redis.Subscribe(ctx, "user:"+user.ID.String())
	defer pubsub.Close()

	// Ping periódico + renovação de presença.
	go func() {
		presenceTicker := time.NewTicker(20 * time.Second)
		pingTicker := time.NewTicker(wsPingInterval)
		defer presenceTicker.Stop()
		defer pingTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-presenceTicker.C:
				_ = s.redis.Set(ctx, presenceKey, "online", 45*time.Second).Err()
			case <-pingTicker.C:
				if wsWrite(websocket.PingMessage, nil) != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Fanout de mensagens recebidas via Redis Pub/Sub.
	go func() {
		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-ch:
				if msg == nil {
					return
				}
				if wsWrite(websocket.TextMessage, []byte(msg.Payload)) != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Reset do deadline de leitura a cada pong recebido.
	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		// Typing indicator: {"type":"typing","conversation_id":"uuid"}
		var peek struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &peek) == nil && peek.Type == "typing" {
			var typingReq struct {
				ConversationID uuid.UUID `json:"conversation_id"`
			}
			if json.Unmarshal(data, &typingReq) == nil && typingReq.ConversationID != uuid.Nil {
				if s.isConversationMember(ctx, typingReq.ConversationID, user.ID) {
					members, _ := s.conversationMembers(ctx, typingReq.ConversationID)
					event, _ := json.Marshal(map[string]any{
						"type":            "typing",
						"conversation_id": typingReq.ConversationID,
						"user_id":         user.ID,
					})
					for _, m := range members {
						if m != user.ID {
							_ = s.redis.Publish(ctx, "user:"+m.String(), event).Err()
						}
					}
				}
			}
			continue
		}

		req, err := parseChatPayloadBytes(data)
		if err != nil {
			_ = wsWriteJSON(map[string]string{"error": err.Error()})
			continue
		}
		if err := validateChatPayload(req); err != nil {
			_ = wsWriteJSON(map[string]string{"error": err.Error()})
			continue
		}
		msg, err := s.createMessage(ctx, user.ID, req)
		if err != nil {
			_ = wsWriteJSON(map[string]string{"error": err.Error()})
			continue
		}
		_ = wsWriteJSON(msg)
	}
}

type chatMessageRequest struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	Text           string    `json:"text"`
}

type chatMessageResponse struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	SenderUserID   uuid.UUID `json:"sender_user_id"`
	Text           string    `json:"text"`
	IsRead         bool      `json:"is_read"`
	CreatedAt      time.Time `json:"created_at"`
}

var (
	errInvalidChatPayload    = errors.New("invalid_chat_payload")
	errNotConversationMember = errors.New("not_conversation_member")
)

func (s *Server) createMessage(ctx context.Context, sender uuid.UUID, req chatMessageRequest) (chatMessageResponse, error) {
	if err := validateChatPayload(req); err != nil {
		return chatMessageResponse{}, err
	}
	if !s.isConversationMember(ctx, req.ConversationID, sender) {
		return chatMessageResponse{}, errNotConversationMember
	}
	var msg chatMessageResponse
	row, err := s.queries.CreateMessage(ctx, db.CreateMessageParams{
		ConversationID: req.ConversationID,
		SenderUserID:   sender,
		Body:           strings.TrimSpace(req.Text),
	})
	if err != nil {
		return chatMessageResponse{}, err
	}
	msg = chatMessageResponse{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		SenderUserID:   row.SenderUserID,
		Text:           row.Body,
		IsRead:         row.IsRead,
		CreatedAt:      timestamptz(row.CreatedAt),
	}
	event, _ := json.Marshal(map[string]any{
		"type":            "message",
		"id":              msg.ID,
		"conversation_id": msg.ConversationID,
		"sender_user_id":  msg.SenderUserID,
		"text":            msg.Text,
		"is_read":         msg.IsRead,
		"created_at":      msg.CreatedAt,
	})
	recipients, err := s.conversationMembers(ctx, req.ConversationID)
	if err == nil {
		for _, recipient := range recipients {
			_ = s.redis.Publish(ctx, "user:"+recipient.String(), event).Err()
		}
	}
	return msg, nil
}

func validateChatPayload(req chatMessageRequest) error {
	text := strings.TrimSpace(req.Text)
	if req.ConversationID == uuid.Nil || len(text) == 0 || len([]rune(text)) > 1000 {
		return errInvalidChatPayload
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "data:") {
		return errors.New("links_and_media_are_not_allowed")
	}
	return nil
}

func (s *Server) recordSwipe(ctx context.Context, actor, target uuid.UUID, action string) (uuid.UUID, uuid.UUID, bool, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)

	err = qtx.CreateSwipe(ctx, db.CreateSwipeParams{
		ActorUserID:  actor,
		TargetUserID: target,
		Action:       action,
	})
	if err != nil || action != "like" {
		if err != nil {
			return uuid.Nil, uuid.Nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, uuid.Nil, false, err
		}
		return uuid.Nil, uuid.Nil, false, nil
	}
	var reciprocal bool
	reciprocal, err = qtx.HasReciprocalLike(ctx, db.HasReciprocalLikeParams{
		ActorUserID:  target,
		TargetUserID: actor,
	})
	if err != nil || !reciprocal {
		if err != nil {
			return uuid.Nil, uuid.Nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, uuid.Nil, false, err
		}
		return uuid.Nil, uuid.Nil, false, nil
	}

	low, high := normalizePair(actor, target)
	var matchID uuid.UUID
	matchID, err = qtx.UpsertMatch(ctx, db.UpsertMatchParams{
		UserLowID:  low,
		UserHighID: high,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	var conversationID uuid.UUID
	conversationID, err = qtx.UpsertConversationForMatch(ctx, matchID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	for _, member := range []uuid.UUID{actor, target} {
		if err := qtx.AddConversationMember(ctx, db.AddConversationMemberParams{
			ConversationID: conversationID,
			UserID:         member,
		}); err != nil {
			return uuid.Nil, uuid.Nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	// Notificar ambos os usuários do novo match via WebSocket com dados do parceiro.
	actorProfile, _ := s.queries.GetPublicProfile(ctx, actor)
	targetProfile, _ := s.queries.GetPublicProfile(ctx, target)
	for _, pair := range []struct {
		recipient uuid.UUID
		partner   any
	}{
		{actor, map[string]any{"id": targetProfile.ID, "display_name": targetProfile.DisplayName, "photo_url": nilIfEmpty(targetProfile.PhotoUrl)}},
		{target, map[string]any{"id": actorProfile.ID, "display_name": actorProfile.DisplayName, "photo_url": nilIfEmpty(actorProfile.PhotoUrl)}},
	} {
		ev, _ := json.Marshal(map[string]any{
			"type":            "match",
			"match_id":        matchID,
			"conversation_id": conversationID,
			"partner":         pair.partner,
		})
		_ = s.redis.Publish(ctx, "user:"+pair.recipient.String(), ev).Err()
	}
	return matchID, conversationID, true, nil
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, r, "missing_session")
			return
		}
		row, err := s.queries.GetUserBySessionTokenHash(r.Context(), auth.HashToken(cookie.Value))
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, "invalid_session")
			return
		}
		if err != nil {
			writeError(w, r, "session_lookup_failed")
			return
		}
		user := currentUser{ID: row.ID, Email: row.Email, Name: row.DisplayName}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(sessionTTL)
	if err := s.queries.CreateSession(r.Context(), db.CreateSessionParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.sessionCookieSecure,
	})
	return nil
}

type googleProfile struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

func (s *Server) fetchGoogleProfile(ctx context.Context, code string) (googleProfile, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", s.googleClientID)
	form.Set("client_secret", s.googleClientSecret)
	form.Set("redirect_uri", s.googleRedirectURL)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return googleProfile{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return googleProfile{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return googleProfile{}, fmt.Errorf("google token status %d: %s", resp.StatusCode, string(body))
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return googleProfile{}, err
	}
	if tokenResp.AccessToken == "" {
		return googleProfile{}, errors.New("missing google access token")
	}

	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return googleProfile{}, err
	}
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := http.DefaultClient.Do(userReq)
	if err != nil {
		return googleProfile{}, err
	}
	defer userResp.Body.Close()
	userBody, _ := io.ReadAll(io.LimitReader(userResp.Body, 1<<20))
	if userResp.StatusCode >= 300 {
		return googleProfile{}, fmt.Errorf("google userinfo status %d: %s", userResp.StatusCode, string(userBody))
	}
	var raw struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := json.Unmarshal(userBody, &raw); err != nil {
		return googleProfile{}, err
	}
	if raw.Sub == "" || raw.Email == "" {
		return googleProfile{}, errors.New("incomplete google profile")
	}
	return googleProfile{
		Subject:       raw.Sub,
		Email:         strings.ToLower(raw.Email),
		EmailVerified: raw.EmailVerified,
		Name:          strings.TrimSpace(raw.Name),
	}, nil
}

func (s *Server) upsertGoogleUser(ctx context.Context, profile googleProfile) (currentUser, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return currentUser{}, err
	}
	defer tx.Rollback(ctx)

	var user currentUser
	qtx := s.queries.WithTx(tx)

	row, err := qtx.GetGoogleUserBySubject(ctx, profile.Subject)
	if err == nil {
		user = currentUser{ID: row.ID, Email: row.Email, Name: row.DisplayName}
		return user, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return currentUser{}, err
	}

	displayName := profile.Name
	if displayName == "" {
		displayName = strings.Split(profile.Email, "@")[0]
	}
	created, err := qtx.UpsertGoogleUserByEmail(ctx, db.UpsertGoogleUserByEmailParams{
		Email:       profile.Email,
		DisplayName: displayName,
	})
	if err != nil {
		return currentUser{}, err
	}
	user = currentUser{ID: created.ID, Email: created.Email, Name: created.DisplayName}
	err = qtx.LinkGoogleIdentity(ctx, db.LinkGoogleIdentityParams{
		UserID:          user.ID,
		ProviderSubject: profile.Subject,
		Email:           profile.Email,
	})
	if err != nil {
		return currentUser{}, err
	}
	return user, tx.Commit(ctx)
}

func (s *Server) emailAllowed(email string) bool {
	if len(s.allowedDomains) == 0 {
		return true
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	_, ok := s.allowedDomains[strings.ToLower(parts[1])]
	return ok
}

func (s *Server) isConversationMember(ctx context.Context, conversationID, userID uuid.UUID) bool {
	exists, err := s.queries.IsConversationMember(ctx, db.IsConversationMemberParams{
		ConversationID: conversationID,
		UserID:         userID,
	})
	return err == nil && exists
}

func (s *Server) conversationMembers(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error) {
	return s.queries.ListConversationMembers(ctx, conversationID)
}

func (s *Server) removeProfilePhotoObject(ctx context.Context, photoURL string) {
	key, ok := s.storage.KeyFromURL(photoURL)
	if !ok {
		return
	}
	_ = s.storage.Delete(ctx, key)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, r, "invalid_json")
		return false
	}
	return true
}

func decodeChatPayload(w http.ResponseWriter, r *http.Request) (chatMessageRequest, bool) {
	defer r.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
		writeError(w, r, "invalid_chat_payload")
		return chatMessageRequest{}, false
	}
	req, err := parseChatPayloadBytes(buf.Bytes())
	if err != nil {
		writeError(w, r, err.Error())
		return chatMessageRequest{}, false
	}
	if err := validateChatPayload(req); err != nil {
		writeError(w, r, err.Error())
		return chatMessageRequest{}, false
	}
	return req, true
}

func parseChatPayloadBytes(data []byte) (chatMessageRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return chatMessageRequest{}, errors.New("invalid_json")
	}
	for key := range raw {
		if key != "conversation_id" && key != "text" {
			return chatMessageRequest{}, errors.New("chat_payload_text_only")
		}
	}
	var req chatMessageRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return chatMessageRequest{}, errors.New("invalid_chat_payload")
	}
	return req, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, code string) {
	def := apperror.Lookup(code)
	writeJSON(w, def.Status, apperror.Response{
		Error: apperror.ErrorBody{
			Code:      def.Code,
			Message:   def.Message,
			RequestID: middleware.GetReqID(r.Context()),
		},
	})
}

func writeChatError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errInvalidChatPayload):
		writeError(w, r, err.Error())
	case errors.Is(err, errNotConversationMember):
		writeError(w, r, err.Error())
	default:
		writeError(w, r, "message_create_failed")
	}
}

func profilePhotoResponses(photos []db.ProfilePhoto) []profilePhotoResponsePayload {
	out := make([]profilePhotoResponsePayload, 0, len(photos))
	for _, photo := range photos {
		out = append(out, profilePhotoResponse(photo))
	}
	return out
}

func profilePhotoResponse(photo db.ProfilePhoto) profilePhotoResponsePayload {
	return profilePhotoResponsePayload{
		ID:        photo.ID,
		UserID:    photo.UserID,
		URL:       photo.Url,
		Position:  photo.Position,
		CreatedAt: timestamptz(photo.CreatedAt),
	}
}

func timestamptz(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func parsePhotoPosition(w http.ResponseWriter, r *http.Request) (int, bool) {
	position, err := strconv.Atoi(chi.URLParam(r, "position"))
	if err != nil || position < 0 || position > 3 {
		writeError(w, r, "invalid_profile_photo_position")
		return 0, false
	}
	return position, true
}

func parseRouteUUID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, key))
	if err != nil {
		writeError(w, r, "invalid_uuid")
		return uuid.Nil, false
	}
	return id, true
}

func mustUser(ctx context.Context) currentUser {
	user, ok := ctx.Value(userContextKey{}).(currentUser)
	if !ok {
		panic("missing authenticated user")
	}
	return user
}

func normalizePair(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if strings.Compare(a.String(), b.String()) < 0 {
		return a, b
	}
	return b, a
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func calcAge(birthDate pgtype.Date) *int {
	if !birthDate.Valid {
		return nil
	}
	now := time.Now()
	years := now.Year() - birthDate.Time.Year()
	birthday := time.Date(now.Year(), birthDate.Time.Month(), birthDate.Time.Day(), 0, 0, 0, 0, time.Local)
	if now.Before(birthday) {
		years--
	}
	return &years
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) isOnline(ctx context.Context, userID uuid.UUID) bool {
	val, err := s.redis.Get(ctx, "presence:"+userID.String()).Result()
	return err == nil && val == "online"
}

func parsePagination(r *http.Request, defaultLimit, maxLimit int) (int, int) {
	limit := defaultLimit
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= maxLimit {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func parseMessagePagination(r *http.Request) (int, pgtype.Timestamptz) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	var before pgtype.Timestamptz
	if t := r.URL.Query().Get("before"); t != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			before = pgtype.Timestamptz{Time: parsed, Valid: true}
		}
	}
	return limit, before
}
