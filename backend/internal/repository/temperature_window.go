package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

func auditActor(audits []*model.AuditLog) string {
	for _, audit := range audits {
		if audit != nil && audit.Actor != "" {
			return audit.Actor
		}
	}
	return "system"
}

func auditRequestID(audits []*model.AuditLog) string {
	for _, audit := range audits {
		if audit != nil && audit.RequestID != "" {
			return audit.RequestID
		}
	}
	return "untracked"
}

// TemperatureWindowRepository owns all persistence operations for 温控规则.
type TemperatureWindowRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.TemperatureWindow], error)
	Get(context.Context, uint) (model.TemperatureWindow, error)
	Create(context.Context, *model.TemperatureWindow) error
	Update(context.Context, uint, uint, *model.TemperatureWindow) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	// RecordActivationBlock stores the latest 生效阻断 metadata on the draft. It
	// never touches status or version, so the draft and any retry keep working.
	RecordActivationBlock(ctx context.Context, id uint, reason string, impact []model.ActivationImpact, blockedAt time.Time) error
	// ActivateWithSupersession moves the draft to active, clears its stale block
	// metadata and marks the previous active rule for the same product class and
	// 场站 as superseded, all in one transaction with the audit trail.
	ActivateWithSupersession(ctx context.Context, id, expectedVersion uint, draft model.TemperatureWindow, productClass, facility string, audits ...*model.AuditLog) error
}

type temperatureWindowRepository struct {
	store *Store[model.TemperatureWindow]
}

func NewTemperatureWindowRepository(db *gorm.DB) TemperatureWindowRepository {
	return &temperatureWindowRepository{store: NewStore[model.TemperatureWindow](db)}
}

func (r *temperatureWindowRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.TemperatureWindow], error) {
	return r.store.List(ctx, q)
}
func (r *temperatureWindowRepository) Get(ctx context.Context, id uint) (model.TemperatureWindow, error) {
	return r.store.Get(ctx, id)
}
func (r *temperatureWindowRepository) Create(ctx context.Context, item *model.TemperatureWindow) error {
	return r.store.Create(ctx, item)
}
func (r *temperatureWindowRepository) Update(ctx context.Context, id, version uint, item *model.TemperatureWindow) error {
	return r.store.Update(ctx, id, version, item)
}
func (r *temperatureWindowRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *temperatureWindowRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

func (r *temperatureWindowRepository) RecordActivationBlock(ctx context.Context, id uint, reason string, impact []model.ActivationImpact, blockedAt time.Time) error {
	if impact == nil {
		impact = []model.ActivationImpact{}
	}
	blockedAt = blockedAt.UTC()
	patch := model.TemperatureWindow{
		BaseModel:       model.BaseModel{UpdatedAt: blockedAt},
		LastBlockedAt:   &blockedAt,
		LastBlockReason: reason,
		LastBlockImpact: impact,
	}
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Struct Updates with an explicit Select keeps the json serializer for
		// last_block_impact active while status and version stay untouched.
		result := tx.Model(&model.TemperatureWindow{}).Where("id = ?", id).
			Select("last_blocked_at", "last_block_reason", "last_block_impact", "updated_at").
			Updates(&patch)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *temperatureWindowRepository) ActivateWithSupersession(ctx context.Context, id, expectedVersion uint, draft model.TemperatureWindow, productClass, facility string, audits ...*model.AuditLog) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Select("*") on the prepared draft clears stale block metadata and
		// bumps status/version in one row; the WHERE clause keeps the
		// optimistic-lock guard identical to the generic Store.Update.
		activate := tx.Model(&model.TemperatureWindow{}).
			Where("id = ? AND version = ? AND status = ?", id, expectedVersion, model.TemperatureWindowInitialStatus).
			Select("*").Omit("id", "code", "created_at", "deleted_at").
			Updates(&draft)
		if activate.Error != nil {
			return activate.Error
		}
		if activate.RowsAffected == 0 {
			return ErrVersionConflict
		}
		// The previous active rule for the same product class and 场站 becomes
		// superseded; every other rule (including the draft itself, already
		// active above) is left untouched.
		var predecessors []model.TemperatureWindow
		if err := tx.Where("status = ? AND product_class = ? AND facility = ? AND id <> ?", "active", productClass, facility, id).Find(&predecessors).Error; err != nil {
			return err
		}
		supersedePatch := model.TemperatureWindow{
			BaseModel: model.BaseModel{Status: "superseded", UpdatedAt: draft.UpdatedAt},
		}
		supersede := tx.Model(&model.TemperatureWindow{}).
			Where("status = ? AND product_class = ? AND facility = ? AND id <> ?", "active", productClass, facility, id).
			Select("status", "updated_at").
			Updates(&supersedePatch)
		if supersede.Error != nil {
			return supersede.Error
		}
		for _, predecessor := range predecessors {
			replaced := model.AuditLog{
				RequestID:   auditRequestID(audits),
				Actor:       auditActor(audits),
				Action:      "transition",
				EntityType:  "TemperatureWindow",
				EntityID:    predecessor.ID,
				BeforeState: "active",
				AfterState:  "superseded",
				Detail:      fmt.Sprintf("superseded by %s for product class %s at %s", draft.Code, productClass, facility),
				CreatedAt:   draft.UpdatedAt,
			}
			if err := tx.Create(&replaced).Error; err != nil {
				return err
			}
		}
		for _, audit := range audits {
			if audit != nil {
				if err := tx.Create(audit).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
