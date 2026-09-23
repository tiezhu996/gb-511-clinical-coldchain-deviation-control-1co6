package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/config"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type stubSecurityService struct{}

func (stubSecurityService) Login(context.Context, dto.LoginRequest) (dto.LoginResponse, error) {
	return dto.LoginResponse{}, nil
}
func (stubSecurityService) Audit(_ context.Context, _, _, _, _ string, _ uint, _, _, _ string) error {
	return nil
}
func (stubSecurityService) ListAudits(context.Context, int, int, string) ([]model.AuditLog, int64, error) {
	return nil, 0, nil
}
func (stubSecurityService) AuditSummary(context.Context, time.Duration) (model.AuditSummary, error) {
	return model.AuditSummary{}, nil
}
func (stubSecurityService) EntityHistory(context.Context, string, uint, int) ([]model.AuditLog, error) {
	return nil, nil
}
func (stubSecurityService) RuntimeConfig() config.PublicConfig { return config.PublicConfig{} }

func newTestServices(t *testing.T) (TemperatureWindowService, *gorm.DB) {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Role{}, &model.User{}, &model.AuditLog{}, &model.SensorEvidence{},
		&model.TransportContainer{}, &model.TemperatureWindow{},
		&model.ExcursionEvent{}, &model.DispositionDecision{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	windowRepo := repository.NewTemperatureWindowRepository(db)
	containerRepo := repository.NewTransportContainerRepository(db)
	excursionRepo := repository.NewExcursionEventRepository(db)
	svc := NewTemperatureWindowService(windowRepo, containerRepo, excursionRepo, stubSecurityService{})
	return svc, db
}

func createWindow(t *testing.T, db *gorm.DB, code, productClass, facility, status string, version uint) model.TemperatureWindow {
	t.Helper()
	item := model.TemperatureWindow{
		BaseModel:           model.BaseModel{Code: code, Name: code + " 规则", Status: status, Version: version},
		ProductClass:        productClass,
		MinimumCelsius:      2,
		MaximumCelsius:      8,
		MaxExcursionMinutes: 15,
		Facility:            facility,
		Owner:               "reviewer",
		Category:            productClass,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create window: %v", err)
	}
	return item
}

func activateRequest(version uint) dto.TransitionRequest {
	return dto.TransitionRequest{Status: "active", ExpectedVersion: version, Reason: "质量负责人复核后生效"}
}

// An in-transit container for the same product class and facility blocks activation and
// leaves the draft and old rule untouched.
func TestActivationBlockedByInTransitContainer(t *testing.T) {
	svc, db := newTestServices(t)
	ctx := context.Background()
	createWindow(t, db, "TW-OLD", "临床样本", "质量体系 QMS-01", "active", 1)
	draft := createWindow(t, db, "TW-NEW", "临床样本", "质量体系 QMS-01", "draft", 1)

	container := model.TransportContainer{
		BaseModel: model.BaseModel{Code: "TC-001", Name: "在途样本箱", Status: "in_transit", Version: 1},
		Facility:  "质量体系 QMS-01",
		Category:  "临床样本",
	}
	if err := db.Create(&container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}

	_, err := svc.Transition(ctx, draft.ID, activateRequest(1), "reviewer", "req-1")
	if err == nil {
		t.Fatal("expected activation to be blocked")
	}
	blocked, ok := err.(*ActivationBlockedError)
	if !ok {
		t.Fatalf("expected *ActivationBlockedError, got %T %v", err, err)
	}
	if len(blocked.Impact.ContainerCodes) != 1 || blocked.Impact.ContainerCodes[0] != "TC-001" {
		t.Fatalf("unexpected container codes: %#v", blocked.Impact.ContainerCodes)
	}
	if blocked.Impact.Blocked != true {
		t.Fatal("impact should be blocked")
	}

	var reloaded model.TemperatureWindow
	if err := db.First(&reloaded, draft.ID).Error; err != nil {
		t.Fatalf("reload draft: %v", err)
	}
	if reloaded.Status != "draft" || reloaded.Version != 1 {
		t.Fatalf("draft mutated during block: status=%s version=%d", reloaded.Status, reloaded.Version)
	}
	if len(reloaded.BlockedContainerCodes) != 1 || reloaded.BlockedContainerCodes[0] != "TC-001" {
		t.Fatalf("persisted block containers not read back: %#v", reloaded.BlockedContainerCodes)
	}
	if reloaded.LastBlockedReason == "" || reloaded.LastBlockedAt.IsZero() {
		t.Fatal("block reason and timestamp must persist for read-back")
	}

	var oldRule model.TemperatureWindow
	if err := db.Where("code = ?", "TW-OLD").First(&oldRule).Error; err != nil {
		t.Fatalf("reload old rule: %v", err)
	}
	if oldRule.Status != "active" {
		t.Fatalf("old rule must remain active, got %s", oldRule.Status)
	}

	var auditCount int64
	db.Model(&model.AuditLog{}).Where("entity_id = ? AND action = ?", draft.ID, "activation_blocked").Count(&auditCount)
	if auditCount != 1 {
		t.Fatalf("expected one activation_blocked audit, got %d", auditCount)
	}
}

// An open excursion linked to the scope window blocks activation and returns its code.
func TestActivationBlockedByOpenExcursion(t *testing.T) {
	svc, db := newTestServices(t)
	ctx := context.Background()
	createWindow(t, db, "TW-OLD", "临床样本", "质量体系 QMS-01", "active", 1)
	draft := createWindow(t, db, "TW-NEW", "临床样本", "质量体系 QMS-01", "draft", 1)

	excursion := model.ExcursionEvent{
		BaseModel: model.BaseModel{Code: "EE-001", Name: "未关闭偏差", Status: "in_review", Version: 1},
		WindowCode: "TW-OLD",
		Facility:   "现场",
	}
	if err := db.Create(&excursion).Error; err != nil {
		t.Fatalf("create excursion: %v", err)
	}

	_, err := svc.Transition(ctx, draft.ID, activateRequest(1), "reviewer", "req-2")
	if err == nil {
		t.Fatal("expected activation to be blocked")
	}
	blocked, ok := err.(*ActivationBlockedError)
	if !ok {
		t.Fatalf("expected *ActivationBlockedError, got %T", err)
	}
	if len(blocked.Impact.ExcursionCodes) != 1 || blocked.Impact.ExcursionCodes[0] != "EE-001" {
		t.Fatalf("unexpected excursion codes: %#v", blocked.Impact.ExcursionCodes)
	}
	if len(blocked.Impact.ContainerCodes) != 0 {
		t.Fatalf("expected no container impact, got %#v", blocked.Impact.ContainerCodes)
	}
}

// An open excursion linked through an in-scope container (even when its window belongs to
// another scope) must also block activation.
func TestActivationBlockedByContainerLinkedExcursion(t *testing.T) {
	svc, db := newTestServices(t)
	ctx := context.Background()
	createWindow(t, db, "TW-OTHER", "临床样本", "质量体系 QMS-09", "active", 1)
	draft := createWindow(t, db, "TW-NEW", "被动保温箱", "沪杭运输线", "draft", 1)

	container := model.TransportContainer{
		BaseModel: model.BaseModel{Code: "TC-009", Name: "在途箱", Status: "in_transit", Version: 1},
		Facility:  "沪杭运输线", Category: "被动保温箱",
	}
	if err := db.Create(&container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}
	excursion := model.ExcursionEvent{
		BaseModel:    model.BaseModel{Code: "EE-009", Name: "容器关联偏差", Status: "open", Version: 1},
		ContainerCode: "TC-009",
		WindowCode:    "TW-OTHER",
	}
	if err := db.Create(&excursion).Error; err != nil {
		t.Fatalf("create excursion: %v", err)
	}

	_, err := svc.Transition(ctx, draft.ID, activateRequest(1), "reviewer", "req-7")
	if err == nil {
		t.Fatal("expected activation to be blocked")
	}
	blocked, ok := err.(*ActivationBlockedError)
	if !ok {
		t.Fatalf("expected *ActivationBlockedError, got %T", err)
	}
	if len(blocked.Impact.ContainerCodes) != 1 || blocked.Impact.ContainerCodes[0] != "TC-009" {
		t.Fatalf("unexpected containers: %#v", blocked.Impact.ContainerCodes)
	}
	if len(blocked.Impact.ExcursionCodes) != 1 || blocked.Impact.ExcursionCodes[0] != "EE-009" {
		t.Fatalf("unexpected excursions: %#v", blocked.Impact.ExcursionCodes)
	}
}

// Once the impact is cleared, resubmitting activates the draft, supersedes the old rule and
// keeps the last block marker readable.
func TestActivationSucceedsAndSupersedes(t *testing.T) {
	svc, db := newTestServices(t)
	ctx := context.Background()
	old := createWindow(t, db, "TW-OLD", "临床样本", "质量体系 QMS-01", "active", 1)
	other := createWindow(t, db, "TW-OTHER", "冻存试剂", "质量体系 QMS-01", "active", 1)
	draft := createWindow(t, db, "TW-NEW", "临床样本", "质量体系 QMS-01", "draft", 1)

	// First attempt is blocked and records the marker.
	container := model.TransportContainer{
		BaseModel: model.BaseModel{Code: "TC-001", Name: "在途样本箱", Status: "in_transit", Version: 1},
		Facility:  "质量体系 QMS-01", Category: "临床样本",
	}
	if err := db.Create(&container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}
	if _, err := svc.Transition(ctx, draft.ID, activateRequest(1), "reviewer", "req-3"); err == nil {
		t.Fatal("expected first activation to be blocked")
	}

	// The in-transit container is cleared and the impact check reports no blockers.
	if err := db.Model(&model.TransportContainer{}).Where("code = ?", "TC-001").Update("status", "cleared").Error; err != nil {
		t.Fatalf("clear container: %v", err)
	}
	impact, err := svc.ActivationImpact(ctx, draft.ID)
	if err != nil {
		t.Fatalf("activation impact: %v", err)
	}
	if impact.Blocked || len(impact.ContainerCodes) != 0 || len(impact.ExcursionCodes) != 0 {
		t.Fatalf("expected clean impact, got %#v", impact)
	}

	activated, err := svc.Transition(ctx, draft.ID, activateRequest(1), "reviewer", "req-4")
	if err != nil {
		t.Fatalf("expected activation to succeed, got %v", err)
	}
	if activated.Status != "active" || activated.Version != 2 {
		t.Fatalf("unexpected activated window: status=%s version=%d", activated.Status, activated.Version)
	}
	if activated.LastBlockedReason == "" || activated.LastBlockedAt.IsZero() || len(activated.BlockedContainerCodes) != 1 {
		t.Fatalf("last block marker must remain readable after activation: reason=%q codes=%#v", activated.LastBlockedReason, activated.BlockedContainerCodes)
	}

	var oldReloaded model.TemperatureWindow
	if err := db.First(&oldReloaded, old.ID).Error; err != nil {
		t.Fatalf("reload old: %v", err)
	}
	if oldReloaded.Status != "superseded" {
		t.Fatalf("old rule must be superseded, got %s", oldReloaded.Status)
	}
	var otherReloaded model.TemperatureWindow
	if err := db.First(&otherReloaded, other.ID).Error; err != nil {
		t.Fatalf("reload other scope: %v", err)
	}
	if otherReloaded.Status != "active" {
		t.Fatalf("different product class rule must stay active, got %s", otherReloaded.Status)
	}

	var transitionAudits int64
	db.Model(&model.AuditLog{}).Where("action = ? AND after_state = ?", "transition", "superseded").Count(&transitionAudits)
	if transitionAudits != 1 {
		t.Fatalf("expected one supersede audit, got %d", transitionAudits)
	}
}

// Rules for a different facility or a closed excursion do not block activation.
func TestActivationIgnoresOtherScope(t *testing.T) {
	svc, db := newTestServices(t)
	ctx := context.Background()
	createWindow(t, db, "TW-OLD", "临床样本", "质量体系 QMS-01", "active", 1)
	draft := createWindow(t, db, "TW-NEW", "临床样本", "质量体系 QMS-01", "draft", 1)

	otherFacilityContainer := model.TransportContainer{
		BaseModel: model.BaseModel{Code: "TC-OTHER", Name: "其他场站", Status: "in_transit", Version: 1},
		Facility:  "其他场站", Category: "临床样本",
	}
	closedExcursion := model.ExcursionEvent{
		BaseModel: model.BaseModel{Code: "EE-CLOSED", Name: "已关闭", Status: "closed", Version: 1},
		WindowCode: "TW-OLD",
	}
	if err := db.Create(&otherFacilityContainer).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}
	if err := db.Create(&closedExcursion).Error; err != nil {
		t.Fatalf("create excursion: %v", err)
	}

	if _, err := svc.Transition(ctx, draft.ID, activateRequest(1), "reviewer", "req-5"); err != nil {
		t.Fatalf("expected activation to succeed, got %v", err)
	}
}

// A stale expectedVersion must not activate even when the impact check is clean.
func TestActivationVersionConflict(t *testing.T) {
	svc, db := newTestServices(t)
	ctx := context.Background()
	draft := createWindow(t, db, "TW-NEW", "临床样本", "质量体系 QMS-01", "draft", 3)
	req := activateRequest(2)
	_, err := svc.Transition(ctx, draft.ID, req, "reviewer", "req-6")
	if err == nil {
		t.Fatal("expected version conflict")
	}
}
