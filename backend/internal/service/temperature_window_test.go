package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/config"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newActivationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(1)"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	tables := []any{
		&model.Role{}, &model.User{}, &model.AuditLog{}, &model.SensorEvidence{},
		&model.TransportContainer{}, &model.TemperatureWindow{},
		&model.ExcursionEvent{}, &model.DispositionDecision{},
	}
	if err := db.AutoMigrate(tables...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		for _, table := range tables {
			_ = db.Migrator().DropTable(table)
		}
	})
	return db
}

func newActivationServices(db *gorm.DB) (TemperatureWindowService, repository.SecurityRepository) {
	securityRepo := repository.NewSecurityRepository(db)
	securitySvc := NewSecurityService(securityRepo, config.Config{JWTSecret: "test-secret", AppName: "test"})
	windowRepo := repository.NewTemperatureWindowRepository(db)
	containerRepo := repository.NewTransportContainerRepository(db)
	excursionRepo := repository.NewExcursionEventRepository(db)
	return NewTemperatureWindowService(windowRepo, containerRepo, excursionRepo, securitySvc), securityRepo
}

func mustCreateWindow(t *testing.T, db *gorm.DB, code, status, productClass, facility string, version uint) model.TemperatureWindow {
	t.Helper()
	window := model.TemperatureWindow{
		BaseModel: model.BaseModel{
			Code: code, Name: code + " 规则", Status: status, Version: version,
		},
		ProductClass: productClass, MinimumCelsius: 2, MaximumCelsius: 8,
		MaxExcursionMinutes: 15, Facility: facility, Owner: "reviewer", Category: productClass,
		MetricUnit: "C", EffectiveAt: time.Now().UTC(),
	}
	if err := db.Create(&window).Error; err != nil {
		t.Fatalf("create window %s: %v", code, err)
	}
	return window
}

func TestActivationBlockedByInTransitContainerAndOpenExcursion(t *testing.T) {
	db := newActivationTestDB(t)
	svc, auditRepo := newActivationServices(db)
	ctx := context.Background()

	active := mustCreateWindow(t, db, "TW-OLD", "active", "临床样本", "沪杭运输线", 1)
	draft := mustCreateWindow(t, db, "TW-NEW", "draft", "临床样本", "沪杭运输线", 1)
	container := model.TransportContainer{
		BaseModel:    model.BaseModel{Code: "TC-900", Name: "在途箱", Status: "in_transit", Version: 1},
		ProductClass: "临床样本", Facility: "沪杭运输线", Owner: "operator", Category: "临床样本",
		SensorID: "SN-900",
	}
	if err := db.Create(&container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}
	excursion := model.ExcursionEvent{
		BaseModel:    model.BaseModel{Code: "EE-900", Name: "未结束偏差", Status: "open", Version: 1},
		ProductClass: "临床样本", Facility: "沪杭运输线", Owner: "reviewer", Category: "高温偏差",
		ContainerCode: "TC-900", WindowCode: "TW-OLD",
	}
	if err := db.Create(&excursion).Error; err != nil {
		t.Fatalf("create excursion: %v", err)
	}

	_, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{Status: "active", ExpectedVersion: draft.Version, Reason: "复核生效"}, "reviewer", "req-blocked")
	if !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("expected ErrActivationBlocked, got %v", err)
	}
	var blockedErr *ActivationBlockedError
	if !errors.As(err, &blockedErr) || len(blockedErr.Window.LastBlockImpact) != 2 {
		t.Fatalf("expected 2 impacts in blocked error, got %+v", err)
	}
	codes := map[string]bool{}
	for _, impact := range blockedErr.Window.LastBlockImpact {
		codes[impact.Code] = true
	}
	if !codes["TC-900"] || !codes["EE-900"] {
		t.Fatalf("expected TC-900 and EE-900 in impacts, got %v", codes)
	}

	// Draft and old active rule must remain untouched.
	var reloadedDraft model.TemperatureWindow
	if err := db.First(&reloadedDraft, draft.ID).Error; err != nil {
		t.Fatalf("reload draft: %v", err)
	}
	if reloadedDraft.Status != "draft" || reloadedDraft.Version != 1 {
		t.Fatalf("draft mutated: status=%s version=%d", reloadedDraft.Status, reloadedDraft.Version)
	}
	if reloadedDraft.LastBlockReason == "" || reloadedDraft.LastBlockedAt == nil || len(reloadedDraft.LastBlockImpact) != 2 {
		t.Fatalf("block metadata not persisted: %+v", reloadedDraft)
	}
	var oldRule model.TemperatureWindow
	if err := db.First(&oldRule, active.ID).Error; err != nil {
		t.Fatalf("reload old rule: %v", err)
	}
	if oldRule.Status != "active" || oldRule.Version != 1 {
		t.Fatalf("old rule mutated: status=%s version=%d", oldRule.Status, oldRule.Version)
	}

	// The block attempt itself must be auditable.
	var blockLogs int64
	if err := db.Model(&model.AuditLog{}).Where("action = ? AND entity_id = ?", "activation_blocked", draft.ID).Count(&blockLogs).Error; err != nil {
		t.Fatalf("count block audits: %v", err)
	}
	if blockLogs != 1 {
		t.Fatalf("expected 1 activation_blocked audit, got %d", blockLogs)
	}
	_ = auditRepo
}

func TestActivationSucceedsAfterImpactsClearedAndSupersedesOldRule(t *testing.T) {
	db := newActivationTestDB(t)
	svc, _ := newActivationServices(db)
	ctx := context.Background()

	active := mustCreateWindow(t, db, "TW-OLD", "active", "临床样本", "沪杭运输线", 3)
	otherActive := mustCreateWindow(t, db, "TW-OTHER", "active", "冻存试剂", "沪杭运输线", 1)
	draft := mustCreateWindow(t, db, "TW-NEW", "draft", "临床样本", "沪杭运输线", 1)
	container := model.TransportContainer{
		BaseModel:    model.BaseModel{Code: "TC-901", Name: "在途箱", Status: "in_transit", Version: 1},
		ProductClass: "临床样本", Facility: "沪杭运输线", Owner: "operator", Category: "临床样本",
		SensorID: "SN-901",
	}
	if err := db.Create(&container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}

	// First submission is blocked, same expectedVersion remains valid for retry.
	if _, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{Status: "active", ExpectedVersion: 1}, "reviewer", "req-1"); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("expected block on first attempt, got %v", err)
	}
	if err := db.Model(&model.TransportContainer{}).Where("code = ?", "TC-901").Update("status", "cleared").Error; err != nil {
		t.Fatalf("clear container: %v", err)
	}

	activated, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{Status: "active", ExpectedVersion: 1, Reason: "影响已处理，再次提交"}, "reviewer", "req-2")
	if err != nil {
		t.Fatalf("retry activation failed: %v", err)
	}
	if activated.Status != "active" || activated.Version != 2 {
		t.Fatalf("draft not activated: status=%s version=%d", activated.Status, activated.Version)
	}
	if activated.LastBlockReason != "" || activated.LastBlockedAt != nil {
		t.Fatalf("block metadata should be cleared after activation: %+v", activated)
	}
	var oldRule model.TemperatureWindow
	if err := db.First(&oldRule, active.ID).Error; err != nil {
		t.Fatalf("reload old rule: %v", err)
	}
	if oldRule.Status != "superseded" {
		t.Fatalf("old rule should be superseded, got %s", oldRule.Status)
	}
	var untouched model.TemperatureWindow
	if err := db.First(&untouched, otherActive.ID).Error; err != nil {
		t.Fatalf("reload other rule: %v", err)
	}
	if untouched.Status != "active" {
		t.Fatalf("other product class rule must stay active, got %s", untouched.Status)
	}

	var audits int64
	if err := db.Model(&model.AuditLog{}).Where("entity_id = ? AND action = ? AND before_state = ? AND after_state = ?", draft.ID, "transition", "draft", "active").Count(&audits).Error; err != nil {
		t.Fatalf("count transition audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("expected 1 draft->active audit, got %d", audits)
	}
}

func TestActivationBlockReasonRemainsReadableAfterUnrelatedScopeClears(t *testing.T) {
	db := newActivationTestDB(t)
	svc, _ := newActivationServices(db)
	ctx := context.Background()

	draft := mustCreateWindow(t, db, "TW-NEW", "draft", "临床样本", "沪杭运输线", 1)
	excursion := model.ExcursionEvent{
		BaseModel:    model.BaseModel{Code: "EE-902", Name: "同场站未结束偏差", Status: "in_review", Version: 1},
		ProductClass: "临床样本", Facility: "沪杭运输线", Owner: "reviewer", Category: "高温偏差",
		ContainerCode: "TC-902", WindowCode: "TW-NEW",
	}
	if err := db.Create(&excursion).Error; err != nil {
		t.Fatalf("create excursion: %v", err)
	}
	// A different facility's closed excursion and ready container must not interfere.
	otherExcursion := model.ExcursionEvent{
		BaseModel:    model.BaseModel{Code: "EE-OTHER", Name: "他场站已关闭偏差", Status: "closed", Version: 1},
		ProductClass: "临床样本", Facility: "北京配送中心", Owner: "reviewer", Category: "高温偏差",
	}
	if err := db.Create(&otherExcursion).Error; err != nil {
		t.Fatalf("create other excursion: %v", err)
	}

	if _, err := svc.Transition(ctx, draft.ID, dto.TransitionRequest{Status: "active", ExpectedVersion: 1}, "reviewer", "req-x"); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("expected block, got %v", err)
	}
	readBack, err := svc.Get(ctx, draft.ID)
	if err != nil {
		t.Fatalf("read back draft: %v", err)
	}
	if readBack.Status != "draft" || readBack.Version != 1 {
		t.Fatalf("draft mutated: %+v", readBack)
	}
	if len(readBack.LastBlockImpact) != 1 || readBack.LastBlockImpact[0].Code != "EE-902" {
		t.Fatalf("expected EE-902 retained as block reason, got %+v", readBack.LastBlockImpact)
	}
}
