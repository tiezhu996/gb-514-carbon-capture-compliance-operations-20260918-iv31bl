package repository

import (
	"context"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"gorm.io/gorm"
)

// CaptureUnitRepository owns all persistence operations for 捕集装置.
type CaptureUnitRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.CaptureUnit], error)
	Get(context.Context, uint) (model.CaptureUnit, error)
	Create(context.Context, *model.CaptureUnit) error
	Update(context.Context, uint, uint, *model.CaptureUnit) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	FindByCode(context.Context, string) (model.CaptureUnit, error)
}

type captureUnitRepository struct {
	store *Store[model.CaptureUnit]
	db    *gorm.DB
}

func NewCaptureUnitRepository(db *gorm.DB) CaptureUnitRepository {
	return &captureUnitRepository{store: NewStore[model.CaptureUnit](db), db: db}
}

func (r *captureUnitRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.CaptureUnit], error) {
	return r.store.List(ctx, q)
}
func (r *captureUnitRepository) Get(ctx context.Context, id uint) (model.CaptureUnit, error) {
	return r.store.Get(ctx, id)
}
func (r *captureUnitRepository) Create(ctx context.Context, item *model.CaptureUnit) error {
	return r.store.Create(ctx, item)
}
func (r *captureUnitRepository) Update(ctx context.Context, id, version uint, item *model.CaptureUnit) error {
	return r.store.Update(ctx, id, version, item)
}
func (r *captureUnitRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *captureUnitRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
func (r *captureUnitRepository) FindByCode(ctx context.Context, code string) (model.CaptureUnit, error) {
	var item model.CaptureUnit
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&item).Error
	return item, err
}
