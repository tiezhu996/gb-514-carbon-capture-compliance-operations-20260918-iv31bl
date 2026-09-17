package repository

import (
	"context"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"gorm.io/gorm"
)

// EmissionSampleRepository owns all persistence operations for 排放样本.
type EmissionSampleRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.EmissionSample], error)
	Get(context.Context, uint) (model.EmissionSample, error)
	Create(context.Context, *model.EmissionSample) error
	Update(context.Context, uint, uint, *model.EmissionSample) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	FindLatestVerifiedByRelatedCode(context.Context, string) (model.EmissionSample, error)
}

type emissionSampleRepository struct {
	store *Store[model.EmissionSample]
	db    *gorm.DB
}

func NewEmissionSampleRepository(db *gorm.DB) EmissionSampleRepository {
	return &emissionSampleRepository{store: NewStore[model.EmissionSample](db), db: db}
}

func (r *emissionSampleRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.EmissionSample], error) {
	return r.store.List(ctx, q)
}
func (r *emissionSampleRepository) Get(ctx context.Context, id uint) (model.EmissionSample, error) {
	return r.store.Get(ctx, id)
}
func (r *emissionSampleRepository) Create(ctx context.Context, item *model.EmissionSample) error {
	return r.store.Create(ctx, item)
}
func (r *emissionSampleRepository) Update(ctx context.Context, id, version uint, item *model.EmissionSample) error {
	return r.store.Update(ctx, id, version, item)
}
func (r *emissionSampleRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *emissionSampleRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

// FindLatestVerifiedByRelatedCode 返回关联装置最新一条已核验（verified）样本；
// collected/testing/invalid 都不能作为复核闭环的证据。
func (r *emissionSampleRepository) FindLatestVerifiedByRelatedCode(ctx context.Context, relatedCode string) (model.EmissionSample, error) {
	var item model.EmissionSample
	err := r.db.WithContext(ctx).
		Where("related_code = ? AND status = ?", relatedCode, "verified").
		Order("effective_at DESC, updated_at DESC, id DESC").First(&item).Error
	return item, err
}
