package repository

import (
	"context"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

// TransportContainerRepository owns all persistence operations for 运输容器.
type TransportContainerRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.TransportContainer], error)
	Get(context.Context, uint) (model.TransportContainer, error)
	Create(context.Context, *model.TransportContainer) error
	Update(context.Context, uint, uint, *model.TransportContainer) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	ListInTransitInScope(context.Context, string, string) ([]model.TransportContainer, error)
}

type transportContainerRepository struct {
	store *Store[model.TransportContainer]
}

func NewTransportContainerRepository(db *gorm.DB) TransportContainerRepository {
	return &transportContainerRepository{store: NewStore[model.TransportContainer](db)}
}

func (r *transportContainerRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.TransportContainer], error) {
	return r.store.List(ctx, q)
}
func (r *transportContainerRepository) Get(ctx context.Context, id uint) (model.TransportContainer, error) {
	return r.store.Get(ctx, id)
}
func (r *transportContainerRepository) Create(ctx context.Context, item *model.TransportContainer) error {
	return r.store.Create(ctx, item)
}
func (r *transportContainerRepository) Update(ctx context.Context, id, version uint, item *model.TransportContainer) error {
	return r.store.Update(ctx, id, version, item)
}
func (r *transportContainerRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *transportContainerRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

// ListInTransitInScope returns containers that are still on the road (status in_transit)
// for the same product class (stored as the container category) and facility.
func (r *transportContainerRepository) ListInTransitInScope(ctx context.Context, productClass, facility string) ([]model.TransportContainer, error) {
	items := make([]model.TransportContainer, 0)
	err := r.store.db.WithContext(ctx).
		Where("status = ? AND LOWER(category) = LOWER(?) AND LOWER(facility) = LOWER(?)", "in_transit", productClass, facility).
		Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}
