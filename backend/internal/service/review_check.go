package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/repository"
)

// reviewGate labels the two moments the spec calls out.
type reviewGate string

const (
	// reviewGateEntry is draft -> review (进入复核).
	reviewGateEntry reviewGate = "entry"
	// reviewGateFinal is review/accepted/escalated -> accepted|escalated (最终判定).
	reviewGateFinal reviewGate = "final"
)

// ReviewGateError is returned when the re-read gate refuses a transition. It
// wraps ErrReviewCheckFailed (for errors.Is mapping) and carries the full
// ReviewCheck snapshot so the handler can echo structured reasons to the page.
type ReviewGateError struct {
	Check dto.ReviewCheck
}

func (e *ReviewGateError) Error() string {
	if len(e.Check.Reasons) > 0 {
		return strings.Join(e.Check.Reasons, "；")
	}
	return ErrReviewCheckFailed.Error()
}

func (e *ReviewGateError) Is(target error) bool { return target == ErrReviewCheckFailed }

// gateRejected builds the typed gate error from a completed check.
func gateRejected(check dto.ReviewCheck) error { return &ReviewGateError{Check: check} }

// reviewCheckService evaluates the compliance review closed loop. It only
// re-reads current data; it never writes, so a failed gate cannot touch any
// evidence, version or audit record.
type reviewCheckService struct {
	contexts repository.ReviewContextRepository
}

func newReviewCheckService(contexts repository.ReviewContextRepository) *reviewCheckService {
	return &reviewCheckService{contexts: contexts}
}

// evaluate re-reads the linked capture unit, the currently effective (active)
// permit rule and the latest verified emission sample by the decision's
// 关联装置 correlation code, then decides what the requested gate allows.
//
// requestedTarget is "" for the read-only preview: the gate is inferred from
// the decision's current state.
func (s *reviewCheckService) evaluate(ctx context.Context, decision model.ComplianceDecision, requestedTarget string) (dto.ReviewCheck, error) {
	relatedCode := strings.ToUpper(strings.TrimSpace(decision.RelatedCode))
	check := dto.ReviewCheck{
		DecisionID:     decision.ID,
		DecisionCode:   decision.Code,
		RelatedCode:    relatedCode,
		CurrentState:   decision.Status,
		Reasons:        make([]string, 0),
		AllowedTargets: make([]string, 0),
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	gate := gateFor(decision.Status, requestedTarget)

	// 1) 关联装置必须可唯一确定。
	unitRef, blocked := s.loadUnit(ctx, relatedCode, decision, &check)
	if !blocked {
		// 2) 当前生效许可规则（active）。
		ruleRef := s.loadActivePermitRule(ctx, relatedCode, unitRef, &check)
		// 3) 最新已核验样本（verified）。
		sampleRef := s.loadLatestVerifiedSample(ctx, relatedCode, unitRef, &check)

		if ruleRef != nil && sampleRef != nil {
			check.PermitRule = ruleRef
			check.Sample = sampleRef
			check.ThresholdValue = ruleRef.MetricValue
			check.ThresholdUnit = ruleRef.MetricUnit
			check.SampleValue = sampleRef.MetricValue
			check.SampleUnit = sampleRef.MetricUnit
			classifyReading(ruleRef, sampleRef, &check)
		}
	}

	resolveAllowedTargets(gate, &check)
	return check, nil
}

func (s *reviewCheckService) loadUnit(ctx context.Context, relatedCode string, decision model.ComplianceDecision, check *dto.ReviewCheck) (*dto.ReviewReference, bool) {
	if relatedCode == "" {
		check.Blocked = true
		check.Reasons = append(check.Reasons, "关联装置缺失：合规决定未填写关联编码，无法按关联装置重新读取许可规则与样本")
		return nil, true
	}
	units, err := s.contexts.CaptureUnitsByRelatedCode(ctx, relatedCode)
	if err != nil {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("关联装置读取失败：%s", relatedCode))
		return nil, true
	}
	if len(units) == 0 {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("关联装置缺失：编码 %s 未对应任何捕集装置，保留原状态", relatedCode))
		return nil, true
	}
	if len(units) > 1 {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("装置不一致：编码 %s 对应 %d 台捕集装置，无法唯一确定复核装置，保留原状态", relatedCode, len(units)))
		return nil, true
	}
	unit := units[0]
	ref := referenceForUnit(unit)
	check.Unit = ref
	if strings.TrimSpace(decision.Facility) != "" && strings.TrimSpace(unit.Facility) != "" &&
		strings.TrimSpace(decision.Facility) != strings.TrimSpace(unit.Facility) {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("装置不一致：决定作业区「%s」与关联装置 %s 作业区「%s」不符，保留原状态",
			strings.TrimSpace(decision.Facility), unit.Code, strings.TrimSpace(unit.Facility)))
		return ref, true
	}
	return ref, false
}

func (s *reviewCheckService) loadActivePermitRule(ctx context.Context, relatedCode string, unitRef *dto.ReviewReference, check *dto.ReviewCheck) *dto.ReviewReference {
	rules, err := s.contexts.ActivePermitRulesByRelatedCode(ctx, relatedCode)
	if err != nil {
		check.Blocked = true
		check.Reasons = append(check.Reasons, "生效许可规则读取失败，保留原状态")
		return nil
	}
	if len(rules) == 0 {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("样本/规则缺失：关联装置 %s 没有当前生效（active）的许可规则，保留原状态", unitRef.Code))
		return nil
	}
	rule := rules[0]
	ref := referenceForRule(rule)
	if strings.TrimSpace(rule.Facility) != "" && strings.TrimSpace(unitRef.Facility) != "" &&
		strings.TrimSpace(rule.Facility) != strings.TrimSpace(unitRef.Facility) {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("装置不一致：生效规则 %s 作业区「%s」与关联装置 %s 作业区「%s」不符，保留原状态",
			rule.Code, strings.TrimSpace(rule.Facility), unitRef.Code, strings.TrimSpace(unitRef.Facility)))
	}
	return ref
}

func (s *reviewCheckService) loadLatestVerifiedSample(ctx context.Context, relatedCode string, unitRef *dto.ReviewReference, check *dto.ReviewCheck) *dto.ReviewReference {
	samples, err := s.contexts.VerifiedSamplesByRelatedCode(ctx, relatedCode)
	if err != nil {
		check.Blocked = true
		check.Reasons = append(check.Reasons, "已核验样本读取失败，保留原状态")
		return nil
	}
	if len(samples) == 0 {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("样本缺失：关联装置 %s 没有已核验（verified）的排放样本，保留原状态", unitRef.Code))
		return nil
	}
	sample := samples[0]
	ref := referenceForSample(sample)
	if strings.TrimSpace(sample.Facility) != "" && strings.TrimSpace(unitRef.Facility) != "" &&
		strings.TrimSpace(sample.Facility) != strings.TrimSpace(unitRef.Facility) {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("装置不一致：最新样本 %s 作业区「%s」与关联装置 %s 作业区「%s」不符，保留原状态",
			sample.Code, strings.TrimSpace(sample.Facility), unitRef.Code, strings.TrimSpace(unitRef.Facility)))
	}
	return ref
}

func classifyReading(ruleRef, sampleRef *dto.ReviewReference, check *dto.ReviewCheck) {
	ruleUnit := strings.TrimSpace(ruleRef.MetricUnit)
	sampleUnit := strings.TrimSpace(sampleRef.MetricUnit)
	if ruleUnit != sampleUnit {
		check.Blocked = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("装置不一致：最新样本 %s 读数单位「%s」与生效规则 %s 阈值单位「%s」无法比对，保留原状态",
			sampleRef.Code, sampleUnit, ruleRef.Code, ruleUnit))
		return
	}
	threshold := ruleRef.MetricValue
	reading := sampleRef.MetricValue
	if reading > threshold {
		check.OverThreshold = true
		check.Reasons = append(check.Reasons, fmt.Sprintf("读数超阈值：最新已核验样本 %s 读数 %s %s 超过生效规则 %s 阈值 %s %s，最终判定只能升级（escalated）",
			sampleRef.Code, formatNumber(reading), sampleUnit, ruleRef.Code, formatNumber(threshold), ruleUnit))
	} else {
		check.Reasons = append(check.Reasons, fmt.Sprintf("读数在阈值内：最新已核验样本 %s 读数 %s %s 未超过生效规则 %s 阈值 %s %s，最终判定只允许复核人接受（accepted）",
			sampleRef.Code, formatNumber(reading), sampleUnit, ruleRef.Code, formatNumber(threshold), ruleUnit))
	}
}

func resolveAllowedTargets(gate reviewGate, check *dto.ReviewCheck) {
	if check.Blocked {
		return
	}
	switch gate {
	case reviewGateEntry:
		// 进入复核只要求装置/规则/样本齐全且一致；超阈值允许进入，留给最终判定处理。
		check.AllowedTargets = append(check.AllowedTargets, "review")
	case reviewGateFinal:
		if check.OverThreshold {
			check.AllowedTargets = append(check.AllowedTargets, "escalated")
		} else {
			check.AllowedTargets = append(check.AllowedTargets, "accepted")
		}
	}
}

// gateFor maps a state + requested target to the relevant gate. A preview
// (empty target) infers the gate from the decision's current state.
func gateFor(currentState, requestedTarget string) reviewGate {
	target := strings.TrimSpace(requestedTarget)
	if target == "review" {
		return reviewGateEntry
	}
	if target == "accepted" || target == "escalated" {
		return reviewGateFinal
	}
	if currentState == "draft" {
		return reviewGateEntry
	}
	return reviewGateFinal
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func referenceForUnit(item model.CaptureUnit) *dto.ReviewReference {
	return &dto.ReviewReference{ID: item.ID, Code: item.Code, Name: item.Name, Status: item.Status,
		Facility: item.Facility, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit, Version: item.Version}
}

func referenceForRule(item model.PermitRule) *dto.ReviewReference {
	return &dto.ReviewReference{ID: item.ID, Code: item.Code, Name: item.Name, Status: item.Status,
		Facility: item.Facility, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit, Version: item.Version}
}

func referenceForSample(item model.EmissionSample) *dto.ReviewReference {
	return &dto.ReviewReference{ID: item.ID, Code: item.Code, Name: item.Name, Status: item.Status,
		Facility: item.Facility, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit, Version: item.Version}
}
