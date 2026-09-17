package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/constants"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/repository"
)

type ComplianceDecisionService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ComplianceDecision], error)
	Get(context.Context, uint) (model.ComplianceDecision, error)
	// ReviewCheck re-reads, by 关联装置, the current effective permit rule and
	// the latest verified sample and returns the read-only gate verdict so the
	// page can explain why a state is preserved or which final move is allowed.
	ReviewCheck(context.Context, uint) (dto.ReviewCheck, error)
	Create(context.Context, dto.CreateComplianceDecision, string, string) (model.ComplianceDecision, error)
	Update(context.Context, uint, dto.UpdateComplianceDecision, string, string) (model.ComplianceDecision, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ComplianceDecision, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type complianceDecisionService struct {
	repository repository.ComplianceDecisionRepository
	security   SecurityService
	review     *reviewCheckService
}

func NewComplianceDecisionService(repo repository.ComplianceDecisionRepository, security SecurityService, reviewContexts repository.ReviewContextRepository) ComplianceDecisionService {
	return &complianceDecisionService{
		repository: repo,
		security:   security,
		review:     newReviewCheckService(reviewContexts),
	}
}

func (s *complianceDecisionService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ComplianceDecision], error) {
	return s.repository.List(ctx, query)
}

func (s *complianceDecisionService) Get(ctx context.Context, id uint) (model.ComplianceDecision, error) {
	return s.repository.Get(ctx, id)
}

// ReviewCheck computes the read-only gate verdict without mutating anything.
func (s *complianceDecisionService) ReviewCheck(ctx context.Context, id uint) (dto.ReviewCheck, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return dto.ReviewCheck{}, err
	}
	return s.review.evaluate(ctx, current, "")
}

func (s *complianceDecisionService) Create(ctx context.Context, input dto.CreateComplianceDecision, actor, requestID string) (model.ComplianceDecision, error) {
	if err := validateComplianceDecisionBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ComplianceDecision{}, err
	}
	item := model.ComplianceDecision{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ComplianceDecisionInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	revision := newDecisionRevision(item.Version, item.Status, item.Evidence, "created compliance decision", actor, requestID)
	if err := s.repository.CreateWithRevision(ctx, &item, revision); err != nil {
		return model.ComplianceDecision{}, fmt.Errorf("create 合规决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "ComplianceDecision", item.ID, "", item.Status, "created 合规决定")
	return s.repository.Get(ctx, item.ID)
}

func (s *complianceDecisionService) Update(ctx context.Context, id uint, input dto.UpdateComplianceDecision, actor, requestID string) (model.ComplianceDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ComplianceDecision{}, err
	}
	if current.Status != string(constants.DecisionStateDraft) {
		return model.ComplianceDecision{}, ErrDecisionLocked
	}
	if err := validateComplianceDecisionBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ComplianceDecision{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	revision := newDecisionRevision(current.Version, current.Status, current.Evidence, "updated draft decision fields", actor, requestID)
	if err := s.repository.UpdateWithRevision(ctx, id, input.ExpectedVersion, &current, revision); err != nil {
		return model.ComplianceDecision{}, fmt.Errorf("update 合规决定: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ComplianceDecision", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *complianceDecisionService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ComplianceDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ComplianceDecision{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.ComplianceDecisionTransitions, current.Status, target) {
		return model.ComplianceDecision{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}

	// 复核闭环：进入复核和最终判定都按关联装置重新读取当前生效许可规则与最新
	// 已核验样本。阈值内最终判定只允许复核人接受；超阈值最终判定只能升级。
	// 复核人/管理员边界在读取门禁之前判定，保持与历史行为一致。
	entryGate := target == string(constants.DecisionStateReview)
	finalGate := target == string(constants.DecisionStateAccepted) || target == string(constants.DecisionStateEscalated)
	if finalGate && role != model.RoleReviewer && role != model.RoleAdmin {
		return model.ComplianceDecision{}, ErrReviewerRequired
	}
	if entryGate || finalGate {
		check, evalErr := s.review.evaluate(ctx, current, target)
		if evalErr != nil {
			return model.ComplianceDecision{}, evalErr
		}
		if check.Blocked || !containsTarget(check.AllowedTargets, target) {
			// 保留原状态：不更新、不追加版本、不写审计。
			return model.ComplianceDecision{}, gateRejected(check)
		}
	}

	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	revision := newDecisionRevision(current.Version, target, current.Evidence, input.Reason, actor, requestID)
	audit := &model.AuditLog{
		RequestID: requestID, Actor: actor, Action: "transition", EntityType: "ComplianceDecision",
		EntityID: id, BeforeState: before, AfterState: target, Detail: input.Reason,
		CreatedAt: time.Now().UTC(),
	}
	// 单次事务提交状态、不可变版本与审计；乐观锁保证并发/重复提交只产生一个
	// 版本，冲突回滚不覆盖既有证据或审计。
	if err := s.repository.TransitionWithRevisionAndAudit(ctx, id, input.ExpectedVersion, &current, revision, audit); err != nil {
		return model.ComplianceDecision{}, fmt.Errorf("transition 合规决定: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func containsTarget(targets []string, target string) bool {
	for _, candidate := range targets {
		if candidate == target {
			return true
		}
	}
	return false
}

func (s *complianceDecisionService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.ComplianceDecisionInitialStatus {
		return ErrDecisionLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ComplianceDecision", id, current.Status, "deleted", "soft deleted 合规决定")
}

func (s *complianceDecisionService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateComplianceDecisionBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}

func newDecisionRevision(version uint, state, evidence, reason, actor, requestID string) *model.DecisionRevision {
	return &model.DecisionRevision{
		Version: version, State: strings.TrimSpace(state), Evidence: strings.TrimSpace(evidence),
		Reason: strings.TrimSpace(reason), Actor: strings.TrimSpace(actor),
		RequestID: strings.TrimSpace(requestID), CreatedAt: time.Now().UTC(),
	}
}
