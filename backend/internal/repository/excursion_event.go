package repository

import (
	"context"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

// ExcursionEventRepository owns all persistence operations for 偏差事件.
type ExcursionEventRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ExcursionEvent], error)
	Get(context.Context, uint) (model.ExcursionEvent, error)
	Create(context.Context, *model.ExcursionEvent) error
	Update(context.Context, uint, uint, *model.ExcursionEvent, ...*model.AuditLog) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	ListUnfinishedInScope(context.Context, string, string) ([]model.ExcursionEvent, error)
}

type excursionEventRepository struct {
	store *Store[model.ExcursionEvent]
}

func NewExcursionEventRepository(db *gorm.DB) ExcursionEventRepository {
	return &excursionEventRepository{store: NewStore[model.ExcursionEvent](db)}
}

func (r *excursionEventRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ExcursionEvent], error) {
	return r.store.List(ctx, q)
}
func (r *excursionEventRepository) Get(ctx context.Context, id uint) (model.ExcursionEvent, error) {
	return r.store.Get(ctx, id)
}
func (r *excursionEventRepository) Create(ctx context.Context, item *model.ExcursionEvent) error {
	return r.store.Create(ctx, item)
}
func (r *excursionEventRepository) Update(ctx context.Context, id, version uint, item *model.ExcursionEvent, audits ...*model.AuditLog) error {
	return r.store.Update(ctx, id, version, item, audits...)
}
func (r *excursionEventRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *excursionEventRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

// ListUnfinishedInScope returns excursions that are not closed (open, in_review or
// decided) and belong to the product class + facility scope. An excursion is in scope when
// it references either a window or a container carrying that product class at that facility.
func (r *excursionEventRepository) ListUnfinishedInScope(ctx context.Context, productClass, facility string) ([]model.ExcursionEvent, error) {
	var windowCodes []string
	if err := r.store.db.WithContext(ctx).Model(&model.TemperatureWindow{}).
		Where("LOWER(product_class) = LOWER(?) AND LOWER(facility) = LOWER(?)", productClass, facility).
		Pluck("code", &windowCodes).Error; err != nil {
		return nil, err
	}
	var containerCodes []string
	if err := r.store.db.WithContext(ctx).Model(&model.TransportContainer{}).
		Where("LOWER(category) = LOWER(?) AND LOWER(facility) = LOWER(?)", productClass, facility).
		Pluck("code", &containerCodes).Error; err != nil {
		return nil, err
	}
	items := make([]model.ExcursionEvent, 0)
	if len(windowCodes) == 0 && len(containerCodes) == 0 {
		return items, nil
	}
	db := r.store.db.WithContext(ctx).Where("status IN ?", []string{"open", "in_review", "decided"})
	if len(windowCodes) > 0 && len(containerCodes) > 0 {
		db = db.Where("window_code IN ? OR container_code IN ?", windowCodes, containerCodes)
	} else if len(windowCodes) > 0 {
		db = db.Where("window_code IN ?", windowCodes)
	} else {
		db = db.Where("container_code IN ?", containerCodes)
	}
	err := db.Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}
