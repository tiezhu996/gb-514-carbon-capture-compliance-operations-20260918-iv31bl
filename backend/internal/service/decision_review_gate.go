package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/repository"
	"gorm.io/gorm"
)

// DecisionReviewGate 在合规决定进入复核与最终判定时，按关联装置重新读取当前生效
// 许可规则与最新已核验样本，给出本时刻允许的唯一操作。任何证据缺陷都必须保留
// 决定原状态，并把原因返回给页面展示。
type DecisionReviewGate interface {
	Evaluate(context.Context, model.ComplianceDecision) model.ReviewGate
}

type decisionReviewGate struct {
	units   repository.CaptureUnitRepository
	rules   repository.PermitRuleRepository
	samples repository.EmissionSampleRepository
}

func NewDecisionReviewGate(
	units repository.CaptureUnitRepository,
	rules repository.PermitRuleRepository,
	samples repository.EmissionSampleRepository,
) DecisionReviewGate {
	return &decisionReviewGate{units: units, rules: rules, samples: samples}
}

func (g *decisionReviewGate) Evaluate(ctx context.Context, decision model.ComplianceDecision) model.ReviewGate {
	gate := model.ReviewGate{Outcome: model.ReviewGateBlocked, Reasons: make([]string, 0, 4), CheckedAt: time.Now().UTC()}
	related := strings.ToUpper(strings.TrimSpace(decision.RelatedCode))
	if related == "" {
		gate.Reasons = append(gate.Reasons, "该决定未关联捕集装置（关联编码为空），无法重读许可规则与排放样本")
		return gate
	}

	unit, err := g.units.FindByCode(ctx, related)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gate.Reasons = append(gate.Reasons, "关联装置 "+related+" 不存在，装置一致性核验未通过")
		} else {
			gate.Reasons = append(gate.Reasons, "重新读取关联装置失败："+err.Error())
		}
		return gate
	}
	gate.UnitCode = unit.Code

	// 装置一致性：决定必须属于装置所在作业区，避免拿别的装置证据决定本装置。
	if !sameFacility(decision.Facility, unit.Facility) {
		gate.Reasons = append(gate.Reasons,
			"装置不一致：决定作业区「"+strings.TrimSpace(decision.Facility)+"」与关联装置 "+unit.Code+" 的作业区「"+strings.TrimSpace(unit.Facility)+"」不符")
	}

	rule, err := g.rules.FindActiveByRelatedCode(ctx, related)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gate.Reasons = append(gate.Reasons, "关联装置 "+related+" 当前没有生效（active）许可规则，阈值无法核验")
		} else {
			gate.Reasons = append(gate.Reasons, "重新读取生效许可规则失败："+err.Error())
		}
		return gate
	}
	gate.RuleCode = rule.Code
	gate.RuleVersion = rule.Version
	gate.RuleThreshold = rule.MetricValue
	gate.MetricUnit = strings.TrimSpace(rule.MetricUnit)

	sample, err := g.samples.FindLatestVerifiedByRelatedCode(ctx, related)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gate.Reasons = append(gate.Reasons, "关联装置 "+related+" 没有已核验（verified）排放样本，缺少可采信读数")
		} else {
			gate.Reasons = append(gate.Reasons, "重新读取最新已核验样本失败："+err.Error())
		}
		return gate
	}
	gate.SampleCode = sample.Code
	gate.SampleReading = sample.MetricValue
	gate.SampleStatus = sample.Status
	if gate.MetricUnit == "" {
		gate.MetricUnit = strings.TrimSpace(sample.MetricUnit)
	} else if sampleUnit := strings.TrimSpace(sample.MetricUnit); sampleUnit != "" && !strings.EqualFold(sampleUnit, gate.MetricUnit) {
		// 单位不一致会让数值比较失去意义，按装置/证据不一致处理，保留原状态。
		gate.Reasons = append(gate.Reasons,
			"装置不一致：样本 "+sample.Code+" 计量单位「"+sampleUnit+"」与生效规则 "+rule.Code+" 计量单位「"+gate.MetricUnit+"」不符")
	}

	if len(gate.Reasons) > 0 {
		return gate
	}

	gate.WithinThreshold = sample.MetricValue <= rule.MetricValue
	if gate.WithinThreshold {
		gate.Outcome = model.ReviewGateReady
		gate.AllowedAction = "accepted"
		return gate
	}
	gate.Outcome = model.ReviewGateExceeded
	gate.AllowedAction = "escalated"
	gate.Reasons = append(gate.Reasons,
		"读数超阈值：最新已核验样本 "+sample.Code+" 读数 "+
			formatReading(sample.MetricValue)+sampleUnitLabel(sample.MetricUnit)+
			" 高于生效规则 "+rule.Code+"（v"+formatVersion(rule.Version)+"）阈值 "+
			formatReading(rule.MetricValue)+sampleUnitLabel(rule.MetricUnit))
	return gate
}

func sameFacility(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func sampleUnitLabel(unit string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return ""
	}
	return " " + unit
}

func formatReading(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatVersion(version uint) string {
	return strconv.FormatUint(uint64(version), 10)
}
