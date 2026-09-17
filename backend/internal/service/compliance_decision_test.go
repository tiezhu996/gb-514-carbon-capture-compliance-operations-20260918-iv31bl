package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/config"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var fixtureSeq atomic.Int64

type decisionFixture struct {
	db       *gorm.DB
	svc      ComplianceDecisionService
	security SecurityService
	ctx      context.Context
	now      time.Time
}

func newDecisionFixture(t *testing.T) decisionFixture {
	t.Helper()
	fixtureSeq.Add(1)
	dsn := fmt.Sprintf("file:decision-%d?mode=memory&cache=shared", fixtureSeq.Load())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.CaptureUnit{}, &model.PermitRule{}, &model.EmissionSample{},
		&model.ComplianceDecision{}, &model.DecisionRevision{},
		&model.AuditLog{}, &model.User{},
	); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	decisionRepo := repository.NewComplianceDecisionRepository(db)
	securityRepo := repository.NewSecurityRepository(db)
	security := NewSecurityService(securityRepo, config.Config{})
	gate := NewDecisionReviewGate(
		repository.NewCaptureUnitRepository(db),
		repository.NewPermitRuleRepository(db),
		repository.NewEmissionSampleRepository(db),
	)
	return decisionFixture{
		db: db, svc: NewComplianceDecisionService(decisionRepo, security, gate),
		security: security, ctx: context.Background(), now: time.Now().UTC(),
	}
}

func (f decisionFixture) seedUnit(code, facility, status string) {
	unit := model.CaptureUnit{
		BaseModel: model.BaseModel{Code: code, Name: "装置 " + code, Status: status, Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		Facility:  facility, Owner: "运行一组", Category: "常规", RiskLevel: "low",
		MetricValue: 10, MetricUnit: "unit", EffectiveAt: f.now, RelatedCode: code,
	}
	if err := f.db.Create(&unit).Error; err != nil {
		fatalf(f, "seed unit: %v", err)
	}
}

func (f decisionFixture) seedRule(code, relatedCode, status string, threshold float64, unit string) {
	rule := model.PermitRule{
		BaseModel: model.BaseModel{Code: code, Name: "规则 " + code, Status: status, Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		Facility:  "作业区-" + relatedCode, Owner: "质量组", Category: "常规", RiskLevel: "medium",
		MetricValue: threshold, MetricUnit: unit, EffectiveAt: f.now, RelatedCode: relatedCode,
	}
	if err := f.db.Create(&rule).Error; err != nil {
		fatalf(f, "seed rule: %v", err)
	}
}

func (f decisionFixture) seedSample(code, relatedCode, status string, reading float64, unit string, effectiveAt time.Time) {
	sample := model.EmissionSample{
		BaseModel: model.BaseModel{Code: code, Name: "样本 " + code, Status: status, Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		Facility:  "作业区-" + relatedCode, Owner: "检测组", Category: "常规", RiskLevel: "medium",
		MetricValue: reading, MetricUnit: unit, EffectiveAt: effectiveAt, RelatedCode: relatedCode,
	}
	if err := f.db.Create(&sample).Error; err != nil {
		fatalf(f, "seed sample: %v", err)
	}
}

// fatalf keeps the seed helpers free of *testing.T plumbing while still failing loudly.
func fatalf(_ decisionFixture, format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}

func TestComplianceDecisionPreservesEveryVersionAndReviewerBoundary(t *testing.T) {
	f := newDecisionFixture(t)
	f.seedUnit("CU-OK", "Capture train A", "running")
	f.seedRule("PR-OK", "CU-OK", "active", 100, "ppm")
	f.seedSample("ES-OK", "CU-OK", "verified", 31.5, "ppm", f.now)

	created, err := f.svc.Create(f.ctx, dto.CreateComplianceDecision{
		Code: "CD-TEST", Name: "Stack compliance decision", Description: "initial assessment",
		Facility: "Capture train A", Owner: "operator", Category: "emissions", RiskLevel: "high",
		MetricValue: 31.5, MetricUnit: "ppm", EffectiveAt: f.now,
		Evidence: "sample ES-OK and permit PR-OK", RelatedCode: "CU-OK",
	}, "operator", "request-create")
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	if created.Version != 1 || len(created.Revisions) != 1 || created.Revisions[0].RequestID != "request-create" {
		t.Fatalf("initial revision context missing: %+v", created.Revisions)
	}

	updated, err := f.svc.Update(f.ctx, created.ID, dto.UpdateComplianceDecision{
		ExpectedVersion: 1, Name: "Stack compliance decision", Description: "expanded assessment",
		Facility: "Capture train A", Owner: "operator", Category: "emissions", RiskLevel: "critical",
		MetricValue: 35, MetricUnit: "ppm", EffectiveAt: f.now,
		Evidence: "sample ES-OK, permit PR-OK, calibrated analyzer", RelatedCode: "CU-OK",
	}, "operator", "request-update")
	if err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if updated.Version != 2 || len(updated.Revisions) != 2 || updated.Revisions[1].Actor != "operator" ||
		updated.Revisions[0].Evidence == updated.Revisions[1].Evidence {
		t.Fatalf("draft revisions were overwritten or incomplete: %+v", updated.Revisions)
	}

	reviewed, err := f.svc.Transition(f.ctx, created.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: 2, Reason: "evidence package is ready for independent review",
	}, "operator", model.RoleOperator, "request-review")
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}
	if reviewed.Status != "review" || len(reviewed.Revisions) != 3 || reviewed.Revisions[2].State != "review" {
		t.Fatalf("review revision missing: %+v", reviewed)
	}

	_, err = f.svc.Transition(f.ctx, created.ID, dto.TransitionRequest{
		Status: "accepted", ExpectedVersion: 3, Reason: "operator attempted final acceptance",
	}, "operator", model.RoleOperator, "request-denied")
	if !errors.Is(err, ErrReviewerRequired) {
		t.Fatalf("expected reviewer boundary, got %v", err)
	}

	accepted, err := f.svc.Transition(f.ctx, created.ID, dto.TransitionRequest{
		Status: "accepted", ExpectedVersion: 3, Reason: "permit threshold and calibrated evidence agree",
	}, "reviewer", model.RoleReviewer, "request-accepted")
	if err != nil {
		t.Fatalf("accept decision: %v", err)
	}
	if accepted.Status != "accepted" || len(accepted.Revisions) != 4 ||
		accepted.Revisions[3].Actor != "reviewer" || accepted.Revisions[3].RequestID != "request-accepted" ||
		accepted.Revisions[0].RequestID != "request-create" {
		t.Fatalf("immutable decision history is incomplete: %+v", accepted.Revisions)
	}

	_, err = f.svc.Update(f.ctx, created.ID, dto.UpdateComplianceDecision{ExpectedVersion: accepted.Version}, "admin", "request-late-update")
	if !errors.Is(err, ErrDecisionLocked) {
		t.Fatalf("expected reviewed decision to be locked, got %v", err)
	}
}

func TestReviewGateWithinThresholdAllowsOnlyAcceptance(t *testing.T) {
	f := newDecisionFixture(t)
	f.seedUnit("CU-IN", "作业区-CU-IN", "running")
	f.seedRule("PR-IN", "CU-IN", "active", 50, "ppm")
	f.seedSample("ES-IN", "CU-IN", "verified", 40, "ppm", f.now)

	created, err := f.svc.Create(f.ctx, decisionInput("CD-IN", "作业区-CU-IN", 40, "CU-IN"), "operator", "r1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	reviewed, err := f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "r2")
	if err != nil {
		t.Fatalf("enter review: %v", err)
	}

	// 阈值内尝试升级：必须被闭环拒绝且保持 review。
	_, err = f.svc.Transition(f.ctx, created.ID, transition("escalated", reviewed.Version), "reviewer", model.RoleReviewer, "r3")
	var gateErr *ReviewGateError
	if !errors.As(err, &gateErr) || gateErr.Gate.Outcome != model.ReviewGateReady {
		t.Fatalf("within-threshold escalation must be blocked, got %v", err)
	}
	assertUnchanged(t, f, created.ID, "review", reviewed.Version, 2)

	// 阈值内复核人接受：成功。
	accepted, err := f.svc.Transition(f.ctx, created.ID, transition("accepted", reviewed.Version), "reviewer", model.RoleReviewer, "r4")
	if err != nil {
		t.Fatalf("accept within threshold: %v", err)
	}
	if accepted.Status != "accepted" || accepted.Version != 3 {
		t.Fatalf("unexpected accepted state: %+v", accepted)
	}
}

func TestReviewGateExceededThresholdForcesEscalation(t *testing.T) {
	f := newDecisionFixture(t)
	f.seedUnit("CU-OVER", "作业区-CU-OVER", "running")
	f.seedRule("PR-OVER", "CU-OVER", "active", 30, "ppm")
	f.seedSample("ES-OVER", "CU-OVER", "verified", 88, "ppm", f.now)

	created, err := f.svc.Create(f.ctx, decisionInput("CD-OVER", "作业区-CU-OVER", 88, "CU-OVER"), "operator", "e1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	reviewed, err := f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "e2")
	if err != nil {
		t.Fatalf("enter review: %v", err)
	}

	// 超阈值接受：只能升级，接受被拒。
	_, err = f.svc.Transition(f.ctx, created.ID, transition("accepted", reviewed.Version), "reviewer", model.RoleReviewer, "e3")
	var gateErr *ReviewGateError
	if !errors.As(err, &gateErr) || gateErr.Gate.Outcome != model.ReviewGateExceeded || len(gateErr.Gate.Reasons) == 0 {
		t.Fatalf("over-threshold acceptance must be forced to escalate, got %v", err)
	}
	assertUnchanged(t, f, created.ID, "review", reviewed.Version, 2)

	escalated, err := f.svc.Transition(f.ctx, created.ID, transition("escalated", reviewed.Version), "reviewer", model.RoleReviewer, "e4")
	if err != nil {
		t.Fatalf("escalate over threshold: %v", err)
	}
	if escalated.Status != "escalated" || escalated.Version != 3 {
		t.Fatalf("unexpected escalated state: %+v", escalated)
	}
}

func TestReviewGateBlocksWhenSampleRuleOrUnitMissing(t *testing.T) {
	cases := []struct {
		name    string
		related string
		setup   func(decisionFixture)
	}{
		{"关联装置不存在", "CU-GHOST", func(decisionFixture) {}},
		{"无生效规则", "CU-NORULE", func(f decisionFixture) {
			f.seedUnit("CU-NORULE", "作业区-CU-NORULE", "running")
		}},
		{"无已核验样本", "CU-NOSAMPLE", func(f decisionFixture) {
			f.seedUnit("CU-NOSAMPLE", "作业区-CU-NOSAMPLE", "running")
			f.seedRule("PR-NOSAMPLE", "CU-NOSAMPLE", "active", 50, "ppm")
		}},
		{"样本仍在检测中", "CU-TESTING", func(f decisionFixture) {
			f.seedUnit("CU-TESTING", "作业区-CU-TESTING", "running")
			f.seedRule("PR-TESTING", "CU-TESTING", "active", 50, "ppm")
			f.seedSample("ES-TESTING", "CU-TESTING", "testing", 20, "ppm", f.now)
		}},
		{"规则已被替代", "CU-OLD", func(f decisionFixture) {
			f.seedUnit("CU-OLD", "作业区-CU-OLD", "running")
			f.seedRule("PR-OLD", "CU-OLD", "superseded", 50, "ppm")
			f.seedSample("ES-OLD", "CU-OLD", "verified", 20, "ppm", f.now)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDecisionFixture(t)
			tc.setup(f)
			created, err := f.svc.Create(f.ctx, decisionInput("CD-"+tc.related, "作业区-"+tc.related, 20, tc.related), "operator", "b1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			// 即使是进入复核，证据缺陷也必须保留 draft 原状态。
			_, err = f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "b2")
			var gateErr *ReviewGateError
			if !errors.As(err, &gateErr) || gateErr.Gate.Outcome != model.ReviewGateBlocked || len(gateErr.Gate.Reasons) == 0 {
				t.Fatalf("expected blocked gate with reasons, got %v", err)
			}
			assertUnchanged(t, f, created.ID, "draft", 1, 1)
		})
	}
}

func TestReviewGateBlocksFacilityMismatch(t *testing.T) {
	f := newDecisionFixture(t)
	f.seedUnit("CU-MIS", "作业区-A", "running")
	f.seedRule("PR-MIS", "CU-MIS", "active", 50, "ppm")
	f.seedSample("ES-MIS", "CU-MIS", "verified", 20, "ppm", f.now)

	created, err := f.svc.Create(f.ctx, decisionInput("CD-MIS", "完全不同的作业区", 20, "CU-MIS"), "operator", "m1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "m2")
	var gateErr *ReviewGateError
	if !errors.As(err, &gateErr) || gateErr.Gate.Outcome != model.ReviewGateBlocked {
		t.Fatalf("facility mismatch must block, got %v", err)
	}
	assertUnchanged(t, f, created.ID, "draft", 1, 1)
}

func TestReviewGateUsesLatestVerifiedSampleAndActiveRule(t *testing.T) {
	f := newDecisionFixture(t)
	f.seedUnit("CU-LATEST", "作业区-CU-LATEST", "running")
	f.seedRule("PR-LATEST", "CU-LATEST", "active", 60, "ppm")
	// 较早的合格样本和较新的超标样本，闭环必须读取“最新已核验”一条。
	f.seedSample("ES-OLD1", "CU-LATEST", "verified", 10, "ppm", f.now.Add(-2*time.Hour))
	f.seedSample("ES-LATE", "CU-LATEST", "verified", 90, "ppm", f.now.Add(-1*time.Hour))
	// 一条更新但未核验的样本不得被采信。
	f.seedSample("ES-PEND", "CU-LATEST", "testing", 5, "ppm", f.now)

	created, err := f.svc.Create(f.ctx, decisionInput("CD-LATEST", "作业区-CU-LATEST", 90, "CU-LATEST"), "operator", "l1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	reviewed, err := f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "l2")
	if err != nil {
		t.Fatalf("enter review: %v", err)
	}
	got, err := f.svc.Get(f.ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ReviewGate == nil || got.ReviewGate.SampleCode != "ES-LATE" || got.ReviewGate.Outcome != model.ReviewGateExceeded {
		t.Fatalf("must re-read latest verified sample: %+v", got.ReviewGate)
	}
	_, err = f.svc.Transition(f.ctx, created.ID, transition("accepted", reviewed.Version), "reviewer", model.RoleReviewer, "l3")
	var gateErr *ReviewGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("latest over-threshold sample must force escalation, got %v", err)
	}
}

func TestConcurrentAndDuplicateSubmissionsCreateSingleVersion(t *testing.T) {
	f := newDecisionFixture(t)
	f.seedUnit("CU-CONC", "作业区-CU-CONC", "running")
	f.seedRule("PR-CONC", "CU-CONC", "active", 80, "ppm")
	f.seedSample("ES-CONC", "CU-CONC", "verified", 30, "ppm", f.now)

	created, err := f.svc.Create(f.ctx, decisionInput("CD-CONC", "作业区-CU-CONC", 30, "CU-CONC"), "operator", "c1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 两个并发请求都以 expectedVersion=1 推进到 review，只允许一个成功。
	type outcome struct {
		err error
	}
	results := make(chan outcome, 2)
	go func() {
		_, e := f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "c2a")
		results <- outcome{e}
	}()
	go func() {
		_, e := f.svc.Transition(f.ctx, created.ID, transition("review", 1), "operator", model.RoleOperator, "c2b")
		results <- outcome{e}
	}()
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch r := <-results; {
		case r.err == nil:
			successes++
		case errors.Is(r.err, repository.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error: %v", r.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one success and one version conflict, got success=%d conflict=%d", successes, conflicts)
	}

	final, err := f.svc.Get(f.ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if final.Status != "review" || final.Version != 2 || len(final.Revisions) != 2 {
		t.Fatalf("duplicate submissions must yield a single new version: %+v", final)
	}

	// 同一审核人用过期 expectedVersion 重复最终判定，不得再生成版本。
	_, err = f.svc.Transition(f.ctx, created.ID, transition("accepted", 1), "reviewer", model.RoleReviewer, "c3-dup")
	if !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("duplicate review with stale version must conflict, got %v", err)
	}
	assertUnchanged(t, f, created.ID, "review", 2, 2)

	// 审计日志只记录一次成功的状态迁移。
	var auditCount int64
	if err := f.db.Model(&model.AuditLog{}).
		Where("entity_type = ? AND entity_id = ? AND action = ?", "ComplianceDecision", created.ID, "transition").
		Count(&auditCount).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("exactly one transition audit must exist, got %d", auditCount)
	}
}

func assertUnchanged(t *testing.T, f decisionFixture, id uint, status string, version, revisionCount uint) {
	t.Helper()
	got, err := f.svc.Get(f.ctx, id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != status || got.Version != version || uint(len(got.Revisions)) != revisionCount {
		t.Fatalf("state must be preserved: status=%s version=%d revisions=%d, want status=%s version=%d revisions=%d",
			got.Status, got.Version, len(got.Revisions), status, version, revisionCount)
	}
}

func decisionInput(code, facility string, metric float64, related string) dto.CreateComplianceDecision {
	now := time.Now().UTC()
	return dto.CreateComplianceDecision{
		Code: code, Name: "闭环复核决定 " + code, Description: "test decision",
		Facility: facility, Owner: "operator", Category: "emissions", RiskLevel: "high",
		MetricValue: metric, MetricUnit: "ppm", EffectiveAt: now,
		Evidence: "linked evidence", RelatedCode: related,
	}
}

func transition(status string, expectedVersion uint) dto.TransitionRequest {
	return dto.TransitionRequest{Status: status, ExpectedVersion: expectedVersion, Reason: "closed-loop review test submission"}
}
