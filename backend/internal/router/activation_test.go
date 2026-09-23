package router

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/config"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/database"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type activationEnvelope struct {
	Data    model.TemperatureWindow `json:"data"`
	Error   string                  `json:"error"`
	Message string                  `json:"message"`
	Meta    struct {
		WindowID        uint                     `json:"windowId"`
		WindowCode      string                   `json:"windowCode"`
		LastBlockReason string                   `json:"lastBlockReason"`
		Impacts         []model.ActivationImpact `json:"impacts"`
	} `json:"meta"`
}

func setupActivationRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := filepath.Join(t.TempDir(), "activation.db")
	cfg := config.Config{
		AppName: "test-app", Environment: "test", JWTSecret: "integration-secret",
		DatabaseDriver: "sqlite", DatabaseDSN: dsn, RequestLimit: 10000,
		TokenTTL: time.Hour,
	}
	db, redisClient, err := database.Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	engine := New(cfg, db, redisClient, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return engine, db
}

func loginToken(t *testing.T, engine *gin.Engine, username string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": "Admin123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: status %d body %s", username, rec.Code, rec.Body.String())
	}
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return payload.Data.Token
}

func transitionWindow(t *testing.T, engine *gin.Engine, token string, id, version uint) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"status": "active", "expectedVersion": version, "reason": "质量复核生效"})
	req := httptest.NewRequest(http.MethodPost, "/api/windows/"+strconv.FormatUint(uint64(id), 10)+"/transition", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func getWindow(t *testing.T, engine *gin.Engine, token string, id uint) model.TemperatureWindow {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/windows/4", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get window: status %d body %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data model.TemperatureWindow `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode window: %v", err)
	}
	return payload.Data
}

// TestActivationImpactCheckOverHTTP exercises the seeded TW-004 draft
// (临床样本 @ 沪杭运输线): TC-002 is in_transit and EE-002 is in_review, so
// activation must fail with 422, return both codes and leave draft/old rule
// untouched. After the impacts are resolved directly, resubmitting activates
// the draft and supersedes the prior active rule.
func TestActivationImpactCheckOverHTTP(t *testing.T) {
	engine, db := setupActivationRouter(t)
	reviewer := loginToken(t, engine, "reviewer")

	rec := transitionWindow(t, engine, reviewer, 4, 1)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	var blocked activationEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &blocked); err != nil {
		t.Fatalf("decode 422: %v", err)
	}
	if blocked.Error != "activation_blocked" {
		t.Fatalf("expected activation_blocked error code, got %q", blocked.Error)
	}
	if blocked.Meta.WindowCode != "TW-004" || blocked.Meta.WindowID != 4 {
		t.Fatalf("unexpected block meta target: %+v", blocked.Meta)
	}
	codes := map[string]string{}
	for _, impact := range blocked.Meta.Impacts {
		codes[impact.Code] = impact.Type
	}
	if codes["TC-002"] != "container" || codes["EE-002"] != "excursion" {
		t.Fatalf("expected TC-002/container and EE-002/excursion in meta, got %v", codes)
	}

	// Read-back must still show the persisted block reason on the unchanged draft.
	readBack := getWindow(t, engine, reviewer, 4)
	if readBack.Status != "draft" || readBack.Version != 1 {
		t.Fatalf("draft changed after block: status=%s version=%d", readBack.Status, readBack.Version)
	}
	if readBack.LastBlockReason == "" || readBack.LastBlockedAt == nil || len(readBack.LastBlockImpact) != 2 {
		t.Fatalf("block reason missing on read-back: %+v", readBack)
	}
	var oldRule model.TemperatureWindow
	if err := db.Where("code = ?", "TW-005").First(&oldRule).Error; err != nil {
		t.Fatalf("load TW-005: %v", err)
	}
	if oldRule.Status != "active" {
		t.Fatalf("same-scope old rule must remain active after blocked activation, got %s", oldRule.Status)
	}
	var otherScopeRule model.TemperatureWindow
	if err := db.Where("code = ?", "TW-001").First(&otherScopeRule).Error; err != nil {
		t.Fatalf("load TW-001: %v", err)
	}
	if otherScopeRule.Status != "active" {
		t.Fatalf("other-scope rule must not be affected by the block, got %s", otherScopeRule.Status)
	}

	// viewer must still be forbidden from the transition (permissions unchanged).
	viewer := loginToken(t, engine, "viewer")
	forbidden := transitionWindow(t, engine, viewer, 4, 1)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("expected viewer 403, got %d", forbidden.Code)
	}

	// Resolve the impacts: container leaves transit and the excursion closes.
	if err := db.Model(&model.TransportContainer{}).Where("code = ?", "TC-002").Update("status", "cleared").Error; err != nil {
		t.Fatalf("clear TC-002: %v", err)
	}
	if err := db.Model(&model.ExcursionEvent{}).Where("code = ?", "EE-002").Update("status", "closed").Error; err != nil {
		t.Fatalf("close EE-002: %v", err)
	}

	rec = transitionWindow(t, engine, reviewer, 4, 1)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected retry 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var activated activationEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &activated); err != nil {
		t.Fatalf("decode activation: %v", err)
	}
	if activated.Data.Status != "active" || activated.Data.Version != 2 {
		t.Fatalf("draft not activated: status=%s version=%d", activated.Data.Status, activated.Data.Version)
	}
	if activated.Data.LastBlockReason != "" || activated.Data.LastBlockedAt != nil {
		t.Fatalf("stale block metadata should be cleared: %+v", activated.Data)
	}
	if err := db.Where("code = ?", "TW-005").First(&oldRule).Error; err != nil {
		t.Fatalf("reload TW-005: %v", err)
	}
	if oldRule.Status != "superseded" {
		t.Fatalf("same-scope old rule should be superseded after retry, got %s", oldRule.Status)
	}
	if err := db.Where("code = ?", "TW-001").First(&otherScopeRule).Error; err != nil {
		t.Fatalf("reload TW-001: %v", err)
	}
	if otherScopeRule.Status != "active" {
		t.Fatalf("other-scope rule TW-001 must stay active, got %s", otherScopeRule.Status)
	}
}
