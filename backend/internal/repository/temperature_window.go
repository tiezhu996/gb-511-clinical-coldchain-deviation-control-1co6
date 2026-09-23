package repository

import (
	"context"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

// TemperatureWindowRepository owns all persistence operations for 温控规则.
type TemperatureWindowRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.TemperatureWindow], error)
	Get(context.Context, uint) (model.TemperatureWindow, error)
	Create(context.Context, *model.TemperatureWindow) error
	Update(context.Context, uint, uint, *model.TemperatureWindow) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	ListActiveInScope(context.Context, uint, string, string) ([]model.TemperatureWindow, error)
	PersistActivationBlock(context.Context, uint, string, string, string, time.Time, *model.AuditLog) error
	ActivateDraft(context.Context, uint, uint, *model.TemperatureWindow, string, string, ...*model.AuditLog) error
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

// ListActiveInScope returns other active rules for the same product class and facility;
// these are the rules a newly activated draft replaces.
func (r *temperatureWindowRepository) ListActiveInScope(ctx context.Context, excludeID uint, productClass, facility string) ([]model.TemperatureWindow, error) {
	items := make([]model.TemperatureWindow, 0)
	err := r.store.db.WithContext(ctx).
		Where("id <> ? AND status = ? AND LOWER(product_class) = LOWER(?) AND LOWER(facility) = LOWER(?)", excludeID, "active", productClass, facility).
		Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

// PersistActivationBlock records why a draft->active transition was refused without
// changing the draft status or version. The audit trail entry is written in the same
// transaction so a failed activation is still traceable via request ID.
func (r *temperatureWindowRepository) PersistActivationBlock(ctx context.Context, id uint, containerCodesJSON, excursionCodesJSON, reason string, blockedAt time.Time, audit *model.AuditLog) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.TemperatureWindow{}).Where("id = ?", id).Updates(map[string]any{
			"last_blocked_container_codes": containerCodesJSON,
			"last_blocked_excursion_codes": excursionCodesJSON,
			"last_blocked_reason":          reason,
			"last_blocked_at":              blockedAt,
			"updated_at":                   blockedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if audit != nil {
			if err := tx.Create(audit).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ActivateDraft turns one draft into the new active rule. In the same transaction every
// other currently active rule for the same product class and facility becomes superseded,
// the block marker on the new rule is kept as a readable history, and all audit entries are
// appended.
func (r *temperatureWindowRepository) ActivateDraft(ctx context.Context, id, expectedVersion uint, draft *model.TemperatureWindow, productClass, facility string, audits ...*model.AuditLog) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		activated := tx.Model(&model.TemperatureWindow{}).
			Where("id = ? AND version = ? AND status = ?", id, expectedVersion, "draft").
			Select("*").Omit("id", "code", "created_at", "deleted_at").Updates(draft)
		if activated.Error != nil {
			return activated.Error
		}
		if activated.RowsAffected == 0 {
			var count int64
			if err := tx.Model(&model.TemperatureWindow{}).Where("id = ?", id).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return gorm.ErrRecordNotFound
			}
			return ErrVersionConflict
		}

		now := draft.UpdatedAt
		superseded := tx.Model(&model.TemperatureWindow{}).
			Where("id <> ? AND status = ? AND LOWER(product_class) = LOWER(?) AND LOWER(facility) = LOWER(?)", id, "active", productClass, facility).
			Updates(map[string]any{"status": "superseded", "updated_at": now})
		if superseded.Error != nil {
			return superseded.Error
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
