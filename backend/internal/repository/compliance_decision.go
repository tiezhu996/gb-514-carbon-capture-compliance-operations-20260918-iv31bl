package repository

import (
	"context"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"gorm.io/gorm"
)

// ComplianceDecisionRepository owns all persistence operations for 合规决定.
type ComplianceDecisionRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ComplianceDecision], error)
	Get(context.Context, uint) (model.ComplianceDecision, error)
	CreateWithRevision(context.Context, *model.ComplianceDecision, *model.DecisionRevision) error
	UpdateWithRevision(context.Context, uint, uint, *model.ComplianceDecision, *model.DecisionRevision) error
	TransitionWithRevisionAndAudit(context.Context, uint, uint, *model.ComplianceDecision, *model.DecisionRevision, *model.AuditLog) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type complianceDecisionRepository struct {
	store *Store[model.ComplianceDecision]
	db    *gorm.DB
}

func NewComplianceDecisionRepository(db *gorm.DB) ComplianceDecisionRepository {
	return &complianceDecisionRepository{store: NewStore[model.ComplianceDecision](db), db: db}
}

func (r *complianceDecisionRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ComplianceDecision], error) {
	page, err := r.store.List(ctx, q)
	if err != nil || len(page.Items) == 0 {
		return page, err
	}
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	var revisions []model.DecisionRevision
	if err := r.db.WithContext(ctx).Where("compliance_decision_id IN ?", ids).
		Order("version ASC").Find(&revisions).Error; err != nil {
		return Page[model.ComplianceDecision]{}, err
	}
	byDecision := make(map[uint][]model.DecisionRevision)
	for _, revision := range revisions {
		byDecision[revision.ComplianceDecisionID] = append(byDecision[revision.ComplianceDecisionID], revision)
	}
	for index := range page.Items {
		page.Items[index].Revisions = byDecision[page.Items[index].ID]
	}
	return page, nil
}
func (r *complianceDecisionRepository) Get(ctx context.Context, id uint) (model.ComplianceDecision, error) {
	var item model.ComplianceDecision
	err := r.db.WithContext(ctx).Preload("Revisions", func(db *gorm.DB) *gorm.DB {
		return db.Order("version ASC")
	}).First(&item, id).Error
	return item, err
}
func (r *complianceDecisionRepository) CreateWithRevision(ctx context.Context, item *model.ComplianceDecision, revision *model.DecisionRevision) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(item).Error; err != nil {
			return err
		}
		revision.ComplianceDecisionID = item.ID
		return tx.Create(revision).Error
	})
}
func (r *complianceDecisionRepository) UpdateWithRevision(ctx context.Context, id, version uint, item *model.ComplianceDecision, revision *model.DecisionRevision) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ComplianceDecision{}).Where("id = ? AND version = ?", id, version).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		revision.ComplianceDecisionID = id
		return tx.Create(revision).Error
	})
}
func (r *complianceDecisionRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}

// TransitionWithRevisionAndAudit 在单个事务内完成乐观锁状态推进、追加不可变版本和
// 写入审计日志。乐观锁 Where version=? 保证并发/重复提交只有一个请求能推进；
// revision 与 audit 均为 INSERT，任何一步失败整体回滚，绝不覆盖既有证据或审计。
func (r *complianceDecisionRepository) TransitionWithRevisionAndAudit(
	ctx context.Context, id, expectedVersion uint,
	item *model.ComplianceDecision, revision *model.DecisionRevision, audit *model.AuditLog,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ComplianceDecision{}).Where("id = ? AND version = ?", id, expectedVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		revision.ComplianceDecisionID = id
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		if audit != nil {
			if err := tx.Create(audit).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *complianceDecisionRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
