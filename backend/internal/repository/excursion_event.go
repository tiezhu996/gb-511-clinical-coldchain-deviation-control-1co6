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
	OpenForScope(context.Context, string, string) ([]model.ExcursionEvent, error)
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

// OpenForScope returns 未结束偏差 (status other than closed) for the given
// product class and 场站, ordered by code for stable impact lists.
func (r *excursionEventRepository) OpenForScope(ctx context.Context, productClass, facility string) ([]model.ExcursionEvent, error) {
	items := make([]model.ExcursionEvent, 0)
	err := r.store.db.WithContext(ctx).Model(&model.ExcursionEvent{}).
		Where("status <> ? AND product_class = ? AND facility = ?", "closed", productClass, facility).
		Order("code ASC").Find(&items).Error
	return items, err
}
