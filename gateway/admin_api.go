package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/websocket"
)

// AdminAPI provides HTTP handlers for gateway management.
type AdminAPI struct {
	router      *Router
	poolManager *PoolManager
	routeConfig *RouteConfigStore
	keyStore    *APIKeyStore
	userStore   *AdminUserStore
	metrics     *Metrics
	server      *Server
	logger      *zap.Logger
}

// NewAdminAPI creates a new AdminAPI.
func NewAdminAPI(
	router *Router,
	poolManager *PoolManager,
	routeConfig *RouteConfigStore,
	keyStore *APIKeyStore,
	userStore *AdminUserStore,
	metrics *Metrics,
	server *Server,
	logger *zap.Logger,
) *AdminAPI {
	return &AdminAPI{
		router:      router,
		poolManager: poolManager,
		routeConfig: routeConfig,
		keyStore:    keyStore,
		userStore:   userStore,
		metrics:     metrics,
		server:      server,
		logger:      logger,
	}
}

// RegisterRoutes mounts admin API endpoints on the given mux.
func (a *AdminAPI) RegisterRoutes(mux *http.ServeMux) {
	auth := AdminAuthMiddleware(a.userStore)

	// Login -- no auth required
	mux.HandleFunc("POST /admin/api/login", a.handleLogin)

	// All other routes require JWT auth
	mux.Handle("GET /admin/api/stats", auth(http.HandlerFunc(a.handleStats)))
	mux.Handle("GET /admin/api/connections", auth(http.HandlerFunc(a.handleConnections)))

	// MT Routes
	mux.Handle("GET /admin/api/routes/mt", auth(http.HandlerFunc(a.handleListMTRoutes)))
	mux.Handle("POST /admin/api/routes/mt", auth(http.HandlerFunc(a.handleCreateMTRoute)))
	mux.Handle("DELETE /admin/api/routes/mt/{prefix}", auth(http.HandlerFunc(a.handleDeleteMTRoute)))

	// MO Routes
	mux.Handle("GET /admin/api/routes/mo", auth(http.HandlerFunc(a.handleListMORoutes)))
	mux.Handle("POST /admin/api/routes/mo", auth(http.HandlerFunc(a.handleCreateMORoute)))
	mux.Handle("DELETE /admin/api/routes/mo/{id}", auth(http.HandlerFunc(a.handleDeleteMORoute)))

	// Pools
	mux.Handle("GET /admin/api/pools", auth(http.HandlerFunc(a.handleListPools)))
	mux.Handle("POST /admin/api/pools", auth(http.HandlerFunc(a.handleCreatePool)))
	mux.Handle("DELETE /admin/api/pools/{name}", auth(http.HandlerFunc(a.handleDeletePool)))

	// API Keys
	mux.Handle("GET /admin/api/apikeys", auth(http.HandlerFunc(a.handleListAPIKeys)))
	mux.Handle("POST /admin/api/apikeys", auth(http.HandlerFunc(a.handleCreateAPIKey)))
	mux.Handle("DELETE /admin/api/apikeys/{id}", auth(http.HandlerFunc(a.handleDeleteAPIKey)))

	// Users
	mux.Handle("GET /admin/api/users", auth(http.HandlerFunc(a.handleListUsers)))
	mux.Handle("POST /admin/api/users", auth(http.HandlerFunc(a.handleCreateUser)))
	mux.Handle("DELETE /admin/api/users/{username}", auth(http.HandlerFunc(a.handleDeleteUser)))
	mux.Handle("PUT /admin/api/users/{username}/password", auth(http.HandlerFunc(a.handleChangePassword)))

	// WebSocket for real-time metrics
	mux.Handle("GET /admin/ws", auth(websocket.Handler(a.handleWebSocket)))
}

// --- Login ---

func (a *AdminAPI) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	token, err := a.userStore.Authenticate(req.Username, req.Password)
	if err != nil {
		a.metrics.AdminLoginTotal.WithLabelValues("failed").Inc()
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	a.metrics.AdminLoginTotal.WithLabelValues("success").Inc()
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// --- Stats ---

func (a *AdminAPI) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]any{
		"pools":       a.poolManager.AllHealth(),
		"connections": a.server.ConnectionCount(),
	}
	if a.router.store != nil {
		stats["store_size"] = a.router.store.MessageCount()
		stats["retry_queue"] = a.router.store.PendingRetryCount()
	}
	writeJSON(w, http.StatusOK, stats)
}

// --- Connections ---

func (a *AdminAPI) handleConnections(w http.ResponseWriter, r *http.Request) {
	conns := a.server.ListConnections()
	writeJSON(w, http.StatusOK, conns)
}

// --- MT Routes CRUD ---

func (a *AdminAPI) handleListMTRoutes(w http.ResponseWriter, r *http.Request) {
	routes := a.router.mtRoutes.ListRoutes()
	writeJSON(w, http.StatusOK, routes)
}

func (a *AdminAPI) handleCreateMTRoute(w http.ResponseWriter, r *http.Request) {
	var route MTRoute
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	a.router.mtRoutes.AddRoute(&route)
	if err := a.routeConfig.SaveMTRoute(&route); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, &route)
}

func (a *AdminAPI) handleDeleteMTRoute(w http.ResponseWriter, r *http.Request) {
	prefix := r.PathValue("prefix")
	a.router.mtRoutes.RemoveRoute(prefix)
	_ = a.routeConfig.DeleteMTRoute(prefix)
	w.WriteHeader(http.StatusNoContent)
}

// --- MO Routes CRUD ---

func (a *AdminAPI) handleListMORoutes(w http.ResponseWriter, r *http.Request) {
	routes := a.router.moRoutes.ListRoutes()
	writeJSON(w, http.StatusOK, routes)
}

func (a *AdminAPI) handleCreateMORoute(w http.ResponseWriter, r *http.Request) {
	var route MORoute
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	a.router.moRoutes.AddRoute(&route)
	if err := a.routeConfig.SaveMORoute(&route); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, &route)
}

func (a *AdminAPI) handleDeleteMORoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// id format: "dest:source" or just "dest" (source defaults to "")
	parts := strings.SplitN(id, ":", 2)
	destPattern := parts[0]
	sourcePrefix := ""
	if len(parts) == 2 {
		sourcePrefix = parts[1]
	}
	a.router.moRoutes.RemoveRoute(destPattern, sourcePrefix)
	_ = a.routeConfig.DeleteMORoute(destPattern, sourcePrefix)
	w.WriteHeader(http.StatusNoContent)
}

// --- Pools CRUD ---

func (a *AdminAPI) handleListPools(w http.ResponseWriter, r *http.Request) {
	health := a.poolManager.AllHealth()
	writeJSON(w, http.StatusOK, health)
}

func (a *AdminAPI) handleCreatePool(w http.ResponseWriter, r *http.Request) {
	var cfg SouthboundPoolConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := a.poolManager.Add(r.Context(), &cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := a.routeConfig.SavePoolConfig(&cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, cfg)
}

func (a *AdminAPI) handleDeletePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_ = a.poolManager.Remove(name)
	_ = a.routeConfig.DeletePoolConfig(name)
	w.WriteHeader(http.StatusNoContent)
}

// --- API Keys CRUD ---

func (a *AdminAPI) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, _ := a.keyStore.List()
	writeJSON(w, http.StatusOK, keys)
}

func (a *AdminAPI) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label     string `json:"label"`
		RateLimit int    `json:"rate_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	key, err := a.keyStore.Create(req.Label, req.RateLimit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"key": key, "label": req.Label})
}

func (a *AdminAPI) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.keyStore.Revoke(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Users CRUD ---

func (a *AdminAPI) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, _ := a.userStore.List()
	writeJSON(w, http.StatusOK, users)
}

func (a *AdminAPI) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Role == "" {
		req.Role = "admin"
	}
	if err := a.userStore.Create(req.Username, req.Password, req.Role); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username, "role": req.Role})
}

func (a *AdminAPI) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if err := a.userStore.Delete(username); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *AdminAPI) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := a.userStore.ChangePassword(username, req.OldPassword, req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- WebSocket real-time metrics ---

// RealtimeMetrics is the struct pushed via WebSocket every 1s.
type RealtimeMetrics struct {
	Timestamp       time.Time    `json:"ts"`
	Pools           []PoolHealth `json:"pools"`
	NorthboundConns int          `json:"northbound_conns"`
	StoreSize       int          `json:"store_size"`
	RetryQueueSize  int          `json:"retry_queue"`
}

func (a *AdminAPI) handleWebSocket(ws *websocket.Conn) {
	defer func() { _ = ws.Close() }()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m := RealtimeMetrics{
			Timestamp:       time.Now(),
			Pools:           a.poolManager.AllHealth(),
			NorthboundConns: a.server.ConnectionCount(),
		}
		if a.router.store != nil {
			m.StoreSize = a.router.store.MessageCount()
			m.RetryQueueSize = a.router.store.PendingRetryCount()
		}
		if err := websocket.JSON.Send(ws, m); err != nil {
			return // Client disconnected
		}
	}
}
