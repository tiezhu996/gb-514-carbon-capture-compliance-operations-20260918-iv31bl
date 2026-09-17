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
	Create(context.Context, dto.CreateComplianceDecision, string, string) (model.ComplianceDecision, error)
	Update(context.Context, uint, dto.UpdateComplianceDecision, string, string) (model.ComplianceDecision, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ComplianceDecision, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type complianceDecisionService struct {
	repository repository.ComplianceDecisionRepository
	security   SecurityService
	gate       DecisionReviewGate
}

func NewComplianceDecisionService(repo repository.ComplianceDecisionRepository, security SecurityService, gate DecisionReviewGate) ComplianceDecisionService {
	return &complianceDecisionService{repository: repo, security: security, gate: gate}
}

func (s *complianceDecisionService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ComplianceDecision], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	for index := range page.Items {
		gate := s.gate.Evaluate(ctx, page.Items[index])
		page.Items[index].ReviewGate = &gate
	}
	return page, nil
}

func (s *complianceDecisionService) Get(ctx context.Context, id uint) (model.ComplianceDecision, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return item, err
	}
	gate := s.gate.Evaluate(ctx, item)
	item.ReviewGate = &gate
	return item, nil
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
	return s.Get(ctx, item.ID)
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
	return s.Get(ctx, id)
}

func (s *complianceDecisionService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ComplianceDecision, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ComplianceDecision{}, err
	}
	target := strings.TrimSpace(input.Status)
	// 乐观锁优先：重复审核或并发提交只要携带过期版本，就直接判定为版本冲突，让调用方
	// 刷新后重试；保证同一版本绝不会生成两次新版本，也不落到状态机分支。
	if input.ExpectedVersion != current.Version {
		return model.ComplianceDecision{}, fmt.Errorf("%w: expected version %d, current %d",
			repository.ErrVersionConflict, input.ExpectedVersion, current.Version)
	}
	if !constants.CanTransition(constants.ComplianceDecisionTransitions, current.Status, target) {
		return model.ComplianceDecision{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}

	// 复核闭环：进入复核或做最终判定时，都按关联装置重新读取当前生效许可规则与
	// 最新已核验样本。退回草稿（review -> draft）不属于判定动作，不施加闭环约束。
	if target != string(constants.DecisionStateDraft) {
		gate := s.gate.Evaluate(ctx, current)
		finalDecision := target == string(constants.DecisionStateAccepted) || target == string(constants.DecisionStateEscalated)

		if finalDecision && role != model.RoleReviewer && role != model.RoleAdmin {
			return model.ComplianceDecision{}, ErrReviewerRequired
		}

		switch gate.Outcome {
		case model.ReviewGateBlocked:
			// 样本缺失或装置不一致：进入复核与最终判定都保留原状态并返回原因。
			return model.ComplianceDecision{}, NewReviewGateError(gate)
		case model.ReviewGateReady:
			// 阈值内只允许复核人接受；进入复核阶段（draft -> review）放行。
			if finalDecision && target != string(constants.DecisionStateAccepted) {
				return model.ComplianceDecision{}, NewReviewGateError(gate)
			}
		case model.ReviewGateExceeded:
			// 读数超阈值只能升级。进入复核阶段时放行，使超标决定带着风险进入待判定，
			// 闭环最终只能由复核人升级处理。
			if finalDecision && target != string(constants.DecisionStateEscalated) {
				return model.ComplianceDecision{}, NewReviewGateError(gate)
			}
		}
	}

	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	revision := newDecisionRevision(current.Version, target, current.Evidence, input.Reason, actor, requestID)
	auditLog := &model.AuditLog{
		Actor: auditActor(actor), RequestID: auditRequestID(requestID), Action: "transition",
		EntityType: "ComplianceDecision", EntityID: id, BeforeState: before, AfterState: target,
		Detail: strings.TrimSpace(input.Reason), CreatedAt: time.Now().UTC(),
	}
	// 乐观锁 + 单事务版本/审计：并发提交和重复审核只能生成一次版本，失败不覆盖
	// 既有证据或审计。
	if err := s.repository.TransitionWithRevisionAndAudit(ctx, id, input.ExpectedVersion, &current, revision, auditLog); err != nil {
		return model.ComplianceDecision{}, fmt.Errorf("transition 合规决定: %w", err)
	}
	return s.Get(ctx, id)
}

func auditActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "system"
	}
	return actor
}

func auditRequestID(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return "untracked"
	}
	return requestID
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
