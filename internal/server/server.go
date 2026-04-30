package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"matchcamp/internal/auth"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const sessionTTL = 30 * 24 * time.Hour
const oauthStateCookieName = "matchcamp_oauth_state"

type Config struct {
	DB                  *pgxpool.Pool
	Redis               *redis.Client
	Log                 *slog.Logger
	SessionCookieName   string
	SessionCookieSecure bool
	AllowedEmailDomains []string
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleRedirectURL   string
}

type Server struct {
	db                  *pgxpool.Pool
	redis               *redis.Client
	log                 *slog.Logger
	sessionCookieName   string
	sessionCookieSecure bool
	allowedDomains      map[string]struct{}
	googleClientID      string
	googleClientSecret  string
	googleRedirectURL   string
	upgrader            websocket.Upgrader
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
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Server{
		db:                  cfg.DB,
		redis:               cfg.Redis,
		log:                 cfg.Log,
		sessionCookieName:   cfg.SessionCookieName,
		sessionCookieSecure: cfg.SessionCookieSecure,
		allowedDomains:      domains,
		googleClientID:      cfg.GoogleClientID,
		googleClientSecret:  cfg.GoogleClientSecret,
		googleRedirectURL:   cfg.GoogleRedirectURL,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/health", s.health)

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
			r.Get("/discovery", s.discovery)
			r.Post("/swipes", s.swipe)
			r.Get("/matches", s.matches)
			r.Get("/conversations", s.conversations)
			r.Get("/conversations/{id}/messages", s.messages)
			r.Post("/conversations/{id}/messages", s.createMessageHTTP)
			r.Get("/ws", s.websocket)
		})
	})

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable")
		return
	}
	if err := s.redis.Ping(ctx).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type registerRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Email == "" || req.DisplayName == "" || len(req.Password) < auth.PasswordMinLen {
		writeError(w, http.StatusBadRequest, "invalid_register_payload")
		return
	}
	if !s.emailAllowed(req.Email) {
		writeError(w, http.StatusForbidden, "email_domain_not_allowed")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "password_hash_failed")
		return
	}
	var user currentUser
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO users (email, display_name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, email, display_name
	`, req.Email, req.DisplayName, hash).Scan(&user.ID, &user.Email, &user.Name)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "email_already_registered")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "register_failed")
		return
	}
	if err := s.createSession(w, r, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_create_failed")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	var user currentUser
	var hash sql.NullString
	err := s.db.QueryRow(r.Context(), `
		SELECT id, email, display_name, password_hash
		FROM users
		WHERE email = $1
	`, req.Email).Scan(&user.ID, &user.Email, &user.Name, &hash)
	if errors.Is(err, pgx.ErrNoRows) || !hash.Valid || !auth.VerifyPassword(hash.String, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "login_failed")
		return
	}
	if err := s.createSession(w, r, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_create_failed")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	if s.googleClientID == "" || s.googleRedirectURL == "" {
		writeError(w, http.StatusServiceUnavailable, "google_oauth_not_configured")
		return
	}
	state, _, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "oauth_state_failed")
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
		writeError(w, http.StatusBadRequest, "invalid_oauth_state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing_oauth_code")
		return
	}
	profile, err := s.fetchGoogleProfile(r.Context(), code)
	if err != nil {
		s.log.Warn("google oauth failed", "error", err)
		writeError(w, http.StatusUnauthorized, "google_oauth_failed")
		return
	}
	if !profile.EmailVerified {
		writeError(w, http.StatusForbidden, "google_email_not_verified")
		return
	}
	if !s.emailAllowed(profile.Email) {
		writeError(w, http.StatusForbidden, "email_domain_not_allowed")
		return
	}
	user, err := s.upsertGoogleUser(r.Context(), profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "google_user_save_failed")
		return
	}
	if err := s.createSession(w, r, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_create_failed")
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
		_, _ = s.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, auth.HashToken(cookie.Value))
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
	writeJSON(w, http.StatusOK, mustUser(r.Context()))
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
		writeError(w, http.StatusBadRequest, "profile_too_large")
		return
	}
	var birthDate any
	if strings.TrimSpace(req.BirthDate) != "" {
		parsed, err := time.Parse("2006-01-02", req.BirthDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_birth_date")
			return
		}
		birthDate = parsed
	}
	_, err := s.db.Exec(r.Context(), `
		INSERT INTO profiles (user_id, bio, course, campus, birth_date)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			bio = EXCLUDED.bio,
			course = EXCLUDED.course,
			campus = EXCLUDED.campus,
			birth_date = EXCLUDED.birth_date,
			updated_at = now()
	`, user.ID, req.Bio, req.Course, req.Campus, birthDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_save_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

type visibilityRequest struct {
	Visible bool `json:"visible"`
}

func (s *Server) updateVisibility(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var req visibilityRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd, err := s.db.Exec(r.Context(), `
		UPDATE profiles
		SET visible = $2, updated_at = now()
		WHERE user_id = $1
		  AND char_length(course) > 0
		  AND char_length(campus) > 0
	`, user.ID, req.Visible)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility_update_failed")
		return
	}
	if cmd.RowsAffected() == 0 {
		writeError(w, http.StatusBadRequest, "profile_incomplete")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"visible": req.Visible})
}

func (s *Server) discovery(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, u.display_name, p.bio, p.course, p.campus
		FROM users u
		JOIN profiles p ON p.user_id = u.id
		WHERE p.visible = true
		  AND u.id <> $1
		  AND NOT EXISTS (
			SELECT 1 FROM swipes sw
			WHERE sw.actor_user_id = $1 AND sw.target_user_id = u.id
		  )
		ORDER BY p.updated_at DESC
		LIMIT 25
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "discovery_failed")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var name, bio, course, campus string
		if err := rows.Scan(&id, &name, &bio, &course, &campus); err != nil {
			writeError(w, http.StatusInternalServerError, "discovery_scan_failed")
			return
		}
		items = append(items, map[string]any{"id": id, "display_name": name, "bio": bio, "course": course, "campus": campus})
	}
	writeJSON(w, http.StatusOK, items)
}

type swipeRequest struct {
	TargetUserID uuid.UUID `json:"target_user_id"`
	Action       string    `json:"action"`
}

func (s *Server) swipe(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var req swipeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TargetUserID == uuid.Nil || req.TargetUserID == user.ID || (req.Action != "like" && req.Action != "pass") {
		writeError(w, http.StatusBadRequest, "invalid_swipe")
		return
	}
	matchID, conversationID, matched, err := s.recordSwipe(r.Context(), user.ID, req.TargetUserID, req.Action)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "swipe_already_exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "swipe_failed")
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
	rows, err := s.db.Query(r.Context(), `
		SELECT m.id, c.id, m.created_at
		FROM matches m
		JOIN conversations c ON c.match_id = m.id
		WHERE m.user_low_id = $1 OR m.user_high_id = $1
		ORDER BY m.created_at DESC
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "matches_failed")
		return
	}
	defer rows.Close()
	writeRows(w, rows, "match_id", "conversation_id", "created_at")
}

func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	rows, err := s.db.Query(r.Context(), `
		SELECT c.id, c.match_id, c.created_at
		FROM conversations c
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE cm.user_id = $1
		ORDER BY c.created_at DESC
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "conversations_failed")
		return
	}
	defer rows.Close()
	writeRows(w, rows, "conversation_id", "match_id", "created_at")
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	conversationID, ok := parseRouteUUID(w, r, "id")
	if !ok {
		return
	}
	if !s.isConversationMember(r.Context(), conversationID, user.ID) {
		writeError(w, http.StatusForbidden, "not_conversation_member")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, sender_user_id, body, created_at
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at ASC
		LIMIT 100
	`, conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "messages_failed")
		return
	}
	defer rows.Close()
	writeRows(w, rows, "id", "sender_user_id", "body", "created_at")
}

func (s *Server) createMessageHTTP(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	conversationID, ok := parseRouteUUID(w, r, "id")
	if !ok {
		return
	}
	req, ok := decodeChatPayload(w, r)
	if !ok {
		return
	}
	if req.ConversationID != conversationID {
		writeError(w, http.StatusBadRequest, "conversation_id_mismatch")
		return
	}
	req.ConversationID = conversationID
	msg, err := s.createMessage(r.Context(), user.ID, req)
	if err != nil {
		writeChatError(w, err)
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
	presenceKey := "presence:" + user.ID.String()
	_ = s.redis.Set(ctx, presenceKey, "online", 45*time.Second).Err()
	defer s.redis.Del(context.Background(), presenceKey)

	pubsub := s.redis.Subscribe(ctx, "user:"+user.ID.String())
	defer pubsub.Close()

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.redis.Set(ctx, presenceKey, "online", 45*time.Second).Err()
			}
		}
	}()

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
				_ = conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload))
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		req, err := parseChatPayloadBytes(data)
		if err != nil {
			_ = conn.WriteJSON(map[string]string{"error": err.Error()})
			continue
		}
		if err := validateChatPayload(req); err != nil {
			_ = conn.WriteJSON(map[string]string{"error": err.Error()})
			continue
		}
		msg, err := s.createMessage(ctx, user.ID, req)
		if err != nil {
			_ = conn.WriteJSON(map[string]string{"error": err.Error()})
			continue
		}
		_ = conn.WriteJSON(msg)
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
	err := s.db.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, sender_user_id, body)
		VALUES ($1, $2, $3)
		RETURNING id, conversation_id, sender_user_id, body, created_at
	`, req.ConversationID, sender, strings.TrimSpace(req.Text)).Scan(&msg.ID, &msg.ConversationID, &msg.SenderUserID, &msg.Text, &msg.CreatedAt)
	if err != nil {
		return chatMessageResponse{}, err
	}
	payload, _ := json.Marshal(msg)
	recipients, err := s.conversationMembers(ctx, req.ConversationID)
	if err == nil {
		for _, recipient := range recipients {
			_ = s.redis.Publish(ctx, "user:"+recipient.String(), payload).Err()
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

	_, err = tx.Exec(ctx, `
		INSERT INTO swipes (actor_user_id, target_user_id, action)
		VALUES ($1, $2, $3)
	`, actor, target, action)
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
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM swipes
			WHERE actor_user_id = $1 AND target_user_id = $2 AND action = 'like'
		)
	`, target, actor).Scan(&reciprocal)
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
	err = tx.QueryRow(ctx, `
		INSERT INTO matches (user_low_id, user_high_id)
		VALUES ($1, $2)
		ON CONFLICT (user_low_id, user_high_id) DO UPDATE SET user_low_id = EXCLUDED.user_low_id
		RETURNING id
	`, low, high).Scan(&matchID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (match_id)
		VALUES ($1)
		ON CONFLICT (match_id) DO UPDATE SET match_id = EXCLUDED.match_id
		RETURNING id
	`, matchID).Scan(&conversationID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	for _, member := range []uuid.UUID{actor, target} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, conversationID, member); err != nil {
			return uuid.Nil, uuid.Nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	return matchID, conversationID, true, nil
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "missing_session")
			return
		}
		var user currentUser
		err = s.db.QueryRow(r.Context(), `
			SELECT u.id, u.email, u.display_name
			FROM sessions s
			JOIN users u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.expires_at > now()
		`, auth.HashToken(cookie.Value)).Scan(&user.ID, &user.Email, &user.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "invalid_session")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "session_lookup_failed")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(sessionTTL)
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, hash, expiresAt); err != nil {
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
	err = tx.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name
		FROM auth_identities ai
		JOIN users u ON u.id = ai.user_id
		WHERE ai.provider = 'google' AND ai.provider_subject = $1
	`, profile.Subject).Scan(&user.ID, &user.Email, &user.Name)
	if err == nil {
		return user, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return currentUser{}, err
	}

	displayName := profile.Name
	if displayName == "" {
		displayName = strings.Split(profile.Email, "@")[0]
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name)
		VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET
			display_name = users.display_name
		RETURNING id, email, display_name
	`, profile.Email, displayName).Scan(&user.ID, &user.Email, &user.Name)
	if err != nil {
		return currentUser{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_identities (user_id, provider, provider_subject, email)
		VALUES ($1, 'google', $2, $3)
		ON CONFLICT (provider, provider_subject) DO NOTHING
	`, user.ID, profile.Subject, profile.Email)
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
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM conversation_members
			WHERE conversation_id = $1 AND user_id = $2
		)
	`, conversationID, userID).Scan(&exists)
	return err == nil && exists
}

func (s *Server) conversationMembers(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id FROM conversation_members WHERE conversation_id = $1`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func decodeChatPayload(w http.ResponseWriter, r *http.Request) (chatMessageRequest, bool) {
	defer r.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_chat_payload")
		return chatMessageRequest{}, false
	}
	req, err := parseChatPayloadBytes(buf.Bytes())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return chatMessageRequest{}, false
	}
	if err := validateChatPayload(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeChatError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidChatPayload):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errNotConversationMember):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "message_create_failed")
	}
}

func writeRows(w http.ResponseWriter, rows pgx.Rows, names ...string) {
	out := make([]map[string]any, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "row_scan_failed")
			return
		}
		item := make(map[string]any, len(names))
		for i, name := range names {
			item[name] = normalizeJSONValue(values[i])
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func normalizeJSONValue(value any) any {
	switch typed := value.(type) {
	case [16]byte:
		return uuid.UUID(typed).String()
	default:
		return value
	}
}

func parseRouteUUID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, key))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_uuid")
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
