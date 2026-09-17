package repository

import (
	"context"
	"strings"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"gorm.io/gorm"
)

// ReviewContextRepository is the read-only boundary used by the compliance
// review loop. Every lookup re-reads the CURRENT effective data by the
// decision's 关联装置 correlation code, instead of trusting snapshots taken
// when the decision draft was created. It never writes to any aggregate.
type ReviewContextRepository interface {
	// CaptureUnitsByRelatedCode returns every capture unit sharing the
	// correlation code. A healthy group has exactly one live unit.
	CaptureUnitsByRelatedCode(context.Context, string) ([]model.CaptureUnit, error)
	// ActivePermitRulesByRelatedCode returns only currently effective
	// (status=active) permit rules, newest first.
	ActivePermitRulesByRelatedCode(context.Context, string) ([]model.PermitRule, error)
	// VerifiedSamplesByRelatedCode returns only verified samples, newest first,
	// so callers can pick the latest 已核验 sample.
	VerifiedSamplesByRelatedCode(context.Context, string) ([]model.EmissionSample, error)
}

type reviewContextRepository struct {
	db *gorm.DB
}

func NewReviewContextRepository(db *gorm.DB) ReviewContextRepository {
	return &reviewContextRepository{db: db}
}

func (r *reviewContextRepository) CaptureUnitsByRelatedCode(ctx context.Context, relatedCode string) ([]model.CaptureUnit, error) {
	items := make([]model.CaptureUnit, 0)
	code := strings.TrimSpace(relatedCode)
	if code == "" {
		return items, nil
	}
	err := r.db.WithContext(ctx).
		Where("related_code = ?", code).
		Order("updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *reviewContextRepository) ActivePermitRulesByRelatedCode(ctx context.Context, relatedCode string) ([]model.PermitRule, error) {
	items := make([]model.PermitRule, 0)
	code := strings.TrimSpace(relatedCode)
	if code == "" {
		return items, nil
	}
	err := r.db.WithContext(ctx).
		Where("related_code = ? AND status = ?", code, "active").
		Order("effective_at DESC, updated_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *reviewContextRepository) VerifiedSamplesByRelatedCode(ctx context.Context, relatedCode string) ([]model.EmissionSample, error) {
	items := make([]model.EmissionSample, 0)
	code := strings.TrimSpace(relatedCode)
	if code == "" {
		return items, nil
	}
	err := r.db.WithContext(ctx).
		Where("related_code = ? AND status = ?", code, "verified").
		Order("effective_at DESC, updated_at DESC, id DESC").Find(&items).Error
	return items, err
}
