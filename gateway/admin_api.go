package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/websocket"
)

// AdminAPI provides HTTP handlers for gateway management.
type AdminAPI struct {
	router          *Router
	poolManager     *PoolManager
	routeConfig     *RouteConfigStore
	keyStore        *APIKeyStore
	userStore       *AdminUserStore
	connConfigStore *ConnectionConfigStore
	metrics         *Metrics
	server          *Server
	logger          *zap.Logger
}

// NewAdminAPI creates a new AdminAPI.
func NewAdminAPI(
	router *Router,
	poolManager *PoolManager,
	routeConfig *RouteConfigStore,
	keyStore *APIKeyStore,
	userStore *AdminUserStore,
	connConfigStore *ConnectionConfigStore,
	metrics *Metrics,
	server *Server,
	logger *zap.Logger,
) *AdminAPI {
	return &AdminAPI{
		router:          router,
		poolManager:     poolManager,
		routeConfig:     routeConfig,
		keyStore:        keyStore,
		userStore:       userStore,
		connConfigStore: connConfigStore,
		metrics:         metrics,
		server:          server,
		logger:          logger,
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
	mux.Handle("GET /admin/api/connections/{id}", auth(http.HandlerFunc(a.handleConnectionDetail)))

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

	// Connection Configs
	mux.Handle("GET /admin/api/connconfigs", auth(http.HandlerFunc(a.handleListConnConfigs)))
	mux.Handle("GET /admin/api/connconfigs/{system_id}", auth(http.HandlerFunc(a.handleGetConnConfig)))
	mux.Handle("POST /admin/api/connconfigs", auth(http.HandlerFunc(a.handleCreateConnConfig)))
	mux.Handle("PUT /admin/api/connconfigs/{system_id}", auth(http.HandlerFunc(a.handleUpdateConnConfig)))
	mux.Handle("DELETE /admin/api/connconfigs/{system_id}", auth(http.HandlerFunc(a.handleDeleteConnConfig)))

	// Message log
	mux.Handle("GET /admin/api/messages", auth(http.HandlerFunc(a.handleListMessages)))

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
		"pools":            a.poolManager.AllHealth(),
		"connections":      a.server.ConnectionCount(),
		"total_submits":    a.router.TotalSubmits(),
		"total_dlrs":       a.router.TotalDLRs(),
		"total_mo":         a.router.TotalMO(),
		"total_forwarded":  a.router.TotalForwarded(),
		"total_throttled":  a.router.TotalThrottled(),
		"affinity_size":    a.router.AffinitySize(),
		"correlation_size": a.router.CorrelationSize(),
		"submit_retries":   a.router.SubmitRetryCount(),
		"pool_names":       a.poolManager.PoolNames(),
	}
	if a.router.store != nil {
		stats["store_size"] = a.router.store.MessageCount()
		stats["retry_queue"] = a.router.store.PendingRetryCount()
		stats["message_log_size"] = a.router.store.MessageLogCount()
	}
	writeJSON(w, http.StatusOK, stats)
}

// --- Connections ---

func (a *AdminAPI) handleConnections(w http.ResponseWriter, r *http.Request) {
	conns := a.server.ListConnections()
	writeJSON(w, http.StatusOK, conns)
}

func (a *AdminAPI) handleConnectionDetail(w http.ResponseWriter, r *http.Request) {
	connID := r.PathValue("id")
	if a.server == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	detail := a.server.GetConnectionDetail(connID)
	if detail == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "connection not found"})
		return
	}
	writeJSON(w, http.StatusOK, detail)
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
	entries := a.poolManager.ListWithHealth()
	writeJSON(w, http.StatusOK, entries)
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

// --- Connection Configs CRUD ---

func (a *AdminAPI) handleListConnConfigs(w http.ResponseWriter, r *http.Request) {
	configs, _ := a.connConfigStore.List()
	writeJSON(w, http.StatusOK, configs)
}

func (a *AdminAPI) handleGetConnConfig(w http.ResponseWriter, r *http.Request) {
	systemID := r.PathValue("system_id")
	cfg, err := a.connConfigStore.Get(systemID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if cfg == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	cfg.Password = "" // Don't expose hash
	writeJSON(w, http.StatusOK, cfg)
}

func (a *AdminAPI) handleCreateConnConfig(w http.ResponseWriter, r *http.Request) {
	var cfg ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := a.connConfigStore.Create(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cfg.Password = "" // Don't return hash
	writeJSON(w, http.StatusCreated, &cfg)
}

func (a *AdminAPI) handleUpdateConnConfig(w http.ResponseWriter, r *http.Request) {
	systemID := r.PathValue("system_id")
	var cfg ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	cfg.SystemID = systemID
	if err := a.connConfigStore.Update(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, err := a.connConfigStore.Get(systemID)
	if err != nil || updated == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve updated config"})
		return
	}
	updated.Password = "" // Don't return hash
	writeJSON(w, http.StatusOK, updated)
}

func (a *AdminAPI) handleDeleteConnConfig(w http.ResponseWriter, r *http.Request) {
	systemID := r.PathValue("system_id")
	if err := a.connConfigStore.Delete(systemID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Message Log ---

func (a *AdminAPI) handleListMessages(w http.ResponseWriter, r *http.Request) {
	filter := MessageFilter{
		ConnID: r.URL.Query().Get("conn_id"),
		Status: r.URL.Query().Get("status"),
		From:   r.URL.Query().Get("from"),
		To:     r.URL.Query().Get("to"),
		Limit:  50,
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			filter.Limit = n
		}
	}
	if v := r.URL.Query().Get("after"); v != "" {
		filter.After, _ = time.Parse(time.RFC3339, v)
	}
	if v := r.URL.Query().Get("before"); v != "" {
		filter.Before, _ = time.Parse(time.RFC3339, v)
	}
	entries, err := a.router.store.QueryMessages(filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, entries)
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
