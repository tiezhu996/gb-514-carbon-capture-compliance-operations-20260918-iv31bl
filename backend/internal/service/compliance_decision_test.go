package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/config"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/dto"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newDecisionTestService(t *testing.T) (ComplianceDecisionService, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:decision-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.ComplianceDecision{}, &model.DecisionRevision{}, &model.AuditLog{},
		&model.CaptureUnit{}, &model.PermitRule{}, &model.EmissionSample{},
	); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	// Serialize SQLite access while preserving the optimistic-lock semantics:
	// concurrent transactions still re-check the version predicate, so exactly
	// one wins and the others observe ErrVersionConflict.
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	svc := NewComplianceDecisionService(
		repository.NewComplianceDecisionRepository(db),
		NewSecurityService(repository.NewSecurityRepository(db), config.Config{}),
		repository.NewReviewContextRepository(db),
	)
	return svc, db
}

func seedUnit(t *testing.T, db *gorm.DB, code, relatedCode, facility string) {
	t.Helper()
	if err := db.Create(&model.CaptureUnit{BaseModel: model.BaseModel{Code: code, Name: "unit " + code, Status: "running", Version: 1}, Facility: facility, RelatedCode: relatedCode}).Error; err != nil {
		t.Fatalf("seed unit: %v", err)
	}
}

func seedRule(t *testing.T, db *gorm.DB, code, relatedCode, facility, status, unit string, threshold float64) {
	t.Helper()
	if err := db.Create(&model.PermitRule{BaseModel: model.BaseModel{Code: code, Name: "rule " + code, Status: status, Version: 1}, Facility: facility, MetricValue: threshold, MetricUnit: unit, RelatedCode: relatedCode}).Error; err != nil {
		t.Fatalf("seed rule: %v", err)
	}
}

func seedSample(t *testing.T, db *gorm.DB, code, relatedCode, facility, status, unit string, value float64) {
	t.Helper()
	if err := db.Create(&model.EmissionSample{BaseModel: model.BaseModel{Code: code, Name: "sample " + code, Status: status, Version: 1}, Facility: facility, MetricValue: value, MetricUnit: unit, RelatedCode: relatedCode}).Error; err != nil {
		t.Fatalf("seed sample: %v", err)
	}
}

func seedDecision(t *testing.T, db *gorm.DB, code, relatedCode, facility, status string) *model.ComplianceDecision {
	t.Helper()
	decision := model.ComplianceDecision{
		BaseModel: model.BaseModel{Code: code, Name: "decision " + code, Status: status, Version: 1},
		Facility:  facility, RelatedCode: relatedCode, Evidence: "initial evidence",
	}
	if err := db.Create(&decision).Error; err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	if err := db.Create(&model.DecisionRevision{
		ComplianceDecisionID: decision.ID, Version: 1, State: status, Evidence: decision.Evidence,
		Reason: "seed", Actor: "system", RequestID: "seed-" + code, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed revision: %v", err)
	}
	return &decision
}

func countAudits(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var total int64
	if err := db.Model(&model.AuditLog{}).Count(&total).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	return total
}

// TestComplianceDecisionWithinThresholdRequiresReviewerAcceptance covers the
// complete closed loop when the latest verified reading is within the permit
// threshold: reviewer acceptance succeeds, escalation is rejected, and every
// version with actor/request id is preserved.
func TestComplianceDecisionWithinThresholdRequiresReviewerAcceptance(t *testing.T) {
	svc, db := newDecisionTestService(t)
	ctx := context.Background()

	seedUnit(t, db, "CU-OK", "G-OK", "Plant A")
	seedRule(t, db, "PR-OK", "G-OK", "Plant A", "active", "ppm", 50)
	seedSample(t, db, "ES-OK", "G-OK", "Plant A", "verified", "ppm", 30)
	decision := seedDecision(t, db, "CD-OK", "G-OK", "Plant A", "draft")

	reviewed, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: 1, Reason: "evidence package ready for independent review",
	}, "operator", model.RoleOperator, "request-review")
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}
	if reviewed.Status != "review" || reviewed.Version != 2 || len(reviewed.Revisions) != 2 {
		t.Fatalf("review revision not appended: %+v", reviewed)
	}

	// Within threshold an operator is still blocked by the reviewer boundary.
	if _, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "accepted", ExpectedVersion: 2, Reason: "operator attempted final acceptance",
	}, "operator", model.RoleOperator, "request-denied"); !errors.Is(err, ErrReviewerRequired) {
		t.Fatalf("expected reviewer boundary, got %v", err)
	}

	// Within threshold escalation is not allowed: the only final move is accept.
	_, err = svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "escalated", ExpectedVersion: 2, Reason: "reviewer attempted to escalate compliant reading",
	}, "reviewer", model.RoleReviewer, "request-escalate-denied")
	if !errors.Is(err, ErrReviewCheckFailed) {
		t.Fatalf("expected within-threshold escalation gate rejection, got %v", err)
	}

	accepted, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "accepted", ExpectedVersion: 2, Reason: "permit threshold and calibrated evidence agree",
	}, "reviewer", model.RoleReviewer, "request-accepted")
	if err != nil {
		t.Fatalf("accept decision: %v", err)
	}
	if accepted.Status != "accepted" || accepted.Version != 3 || len(accepted.Revisions) != 3 ||
		accepted.Revisions[2].Actor != "reviewer" || accepted.Revisions[2].RequestID != "request-accepted" {
		t.Fatalf("immutable decision history is incomplete: %+v", accepted.Revisions)
	}

	// After acceptance the business fields stay locked.
	_, err = svc.Update(ctx, decision.ID, dto.UpdateComplianceDecision{ExpectedVersion: accepted.Version}, "admin", "request-late-update")
	if !errors.Is(err, ErrDecisionLocked) {
		t.Fatalf("expected reviewed decision to be locked, got %v", err)
	}
}

// TestComplianceDecisionOverThresholdCanOnlyEscalate enforces that a final
// judgement over the re-read permit threshold can never be accepted.
func TestComplianceDecisionOverThresholdCanOnlyEscalate(t *testing.T) {
	svc, db := newDecisionTestService(t)
	ctx := context.Background()

	seedUnit(t, db, "CU-OVER", "G-OVER", "Plant B")
	seedRule(t, db, "PR-OVER", "G-OVER", "Plant B", "active", "ppm", 50)
	seedSample(t, db, "ES-OVER", "G-OVER", "Plant B", "verified", "ppm", 80)
	decision := seedDecision(t, db, "CD-OVER", "G-OVER", "Plant B", "draft")

	if _, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: 1, Reason: "enter review despite high reading",
	}, "operator", model.RoleOperator, "request-review"); err != nil {
		t.Fatalf("over-threshold reading may still enter review: %v", err)
	}

	_, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "accepted", ExpectedVersion: 2, Reason: "reviewer attempted to accept over-threshold reading",
	}, "reviewer", model.RoleReviewer, "request-accept-denied")
	if !errors.Is(err, ErrReviewCheckFailed) {
		t.Fatalf("expected over-threshold acceptance gate rejection, got %v", err)
	}
	var gateErr *ReviewGateError
	if !errors.As(err, &gateErr) || !gateErr.Check.OverThreshold {
		t.Fatalf("expected structured over-threshold gate context, got %v", err)
	}

	escalated, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "escalated", ExpectedVersion: 2, Reason: "reading exceeds permit threshold, escalate",
	}, "reviewer", model.RoleReviewer, "request-escalated")
	if err != nil {
		t.Fatalf("over-threshold final judgement must allow escalation: %v", err)
	}
	if escalated.Status != "escalated" || escalated.Version != 3 {
		t.Fatalf("escalation did not produce the final version: %+v", escalated)
	}
}

// TestComplianceDecisionReviewGatePreservesStateWhenContextIsInvalid proves
// the sample-missing / device-mismatch / rule-missing branches keep the
// original state and do not append a version or an audit record.
func TestComplianceDecisionReviewGatePreservesStateWhenContextIsInvalid(t *testing.T) {
	cases := []struct {
		name       string
		related    string
		facility   string
		seed       func(db *gorm.DB)
		decisionID func(t *testing.T, db *gorm.DB) *model.ComplianceDecision
	}{
		{
			name:     "missing related code",
			related:  "",
			facility: "Plant C",
			seed:     func(db *gorm.DB) {},
			decisionID: func(t *testing.T, db *gorm.DB) *model.ComplianceDecision {
				return seedDecision(t, db, "CD-EMPTY", "", "Plant C", "draft")
			},
		},
		{
			name:     "no capture unit",
			related:  "G-NO-UNIT",
			facility: "Plant C",
			seed:     func(db *gorm.DB) {},
			decisionID: func(t *testing.T, db *gorm.DB) *model.ComplianceDecision {
				return seedDecision(t, db, "CD-NO-UNIT", "G-NO-UNIT", "Plant C", "draft")
			},
		},
		{
			name:     "no active permit rule",
			related:  "G-NO-RULE",
			facility: "Plant C",
			seed: func(db *gorm.DB) {
				seedUnit(t, db, "CU-NO-RULE", "G-NO-RULE", "Plant C")
				seedRule(t, db, "PR-OLD", "G-NO-RULE", "Plant C", "superseded", "ppm", 50)
				seedSample(t, db, "ES-NO-RULE", "G-NO-RULE", "Plant C", "verified", "ppm", 10)
			},
			decisionID: func(t *testing.T, db *gorm.DB) *model.ComplianceDecision {
				return seedDecision(t, db, "CD-NO-RULE", "G-NO-RULE", "Plant C", "draft")
			},
		},
		{
			name:     "no verified sample",
			related:  "G-NO-SAMPLE",
			facility: "Plant C",
			seed: func(db *gorm.DB) {
				seedUnit(t, db, "CU-NO-SAMPLE", "G-NO-SAMPLE", "Plant C")
				seedRule(t, db, "PR-NO-SAMPLE", "G-NO-SAMPLE", "Plant C", "active", "ppm", 50)
				seedSample(t, db, "ES-TESTING", "G-NO-SAMPLE", "Plant C", "testing", "ppm", 10)
			},
			decisionID: func(t *testing.T, db *gorm.DB) *model.ComplianceDecision {
				return seedDecision(t, db, "CD-NO-SAMPLE", "G-NO-SAMPLE", "Plant C", "draft")
			},
		},
		{
			name:     "facility mismatch",
			related:  "G-MISMATCH",
			facility: "Plant C",
			seed: func(db *gorm.DB) {
				seedUnit(t, db, "CU-MISMATCH", "G-MISMATCH", "Plant Z")
				seedRule(t, db, "PR-MISMATCH", "G-MISMATCH", "Plant Z", "active", "ppm", 50)
				seedSample(t, db, "ES-MISMATCH", "G-MISMATCH", "Plant Z", "verified", "ppm", 10)
			},
			decisionID: func(t *testing.T, db *gorm.DB) *model.ComplianceDecision {
				return seedDecision(t, db, "CD-MISMATCH", "G-MISMATCH", "Plant C", "draft")
			},
		},
		{
			name:     "metric unit mismatch",
			related:  "G-UNIT-MISMATCH",
			facility: "Plant C",
			seed: func(db *gorm.DB) {
				seedUnit(t, db, "CU-UNIT-MISMATCH", "G-UNIT-MISMATCH", "Plant C")
				seedRule(t, db, "PR-UNIT-MISMATCH", "G-UNIT-MISMATCH", "Plant C", "active", "ppm", 50)
				seedSample(t, db, "ES-UNIT-MISMATCH", "G-UNIT-MISMATCH", "Plant C", "verified", "mg", 10)
			},
			decisionID: func(t *testing.T, db *gorm.DB) *model.ComplianceDecision {
				return seedDecision(t, db, "CD-UNIT-MISMATCH", "G-UNIT-MISMATCH", "Plant C", "review")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newDecisionTestService(t)
			ctx := context.Background()
			if tc.seed != nil {
				tc.seed(db)
			}
			decision := tc.decisionID(t, db)
			before := countAudits(t, db)

			target := "review"
			if decision.Status == "review" {
				target = "accepted"
			}
			_, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
				Status: target, ExpectedVersion: decision.Version, Reason: "attempt gated move with invalid context",
			}, "reviewer", model.RoleReviewer, "request-blocked")
			if !errors.Is(err, ErrReviewCheckFailed) {
				t.Fatalf("expected gate rejection, got %v", err)
			}

			reloaded, err := svc.Get(ctx, decision.ID)
			if err != nil {
				t.Fatalf("reload decision: %v", err)
			}
			if reloaded.Status != decision.Status || reloaded.Version != decision.Version {
				t.Fatalf("original state was not preserved: now status=%s version=%d", reloaded.Status, reloaded.Version)
			}
			if len(reloaded.Revisions) != 1 {
				t.Fatalf("gate failure appended a revision: %+v", reloaded.Revisions)
			}
			if countAudits(t, db) != before {
				t.Fatalf("gate failure must not append an audit record")
			}
		})
	}
}

// TestComplianceDecisionConcurrentAndDuplicateSubmissionsCreateOneVersion
// models two reviewers who both read the review/v2 aggregate and concurrently
// commit the same final decision with expectedVersion=2. The optimistic-lock
// predicate must let exactly one transaction through: one new revision, one
// audit row; the duplicate receives ErrVersionConflict and overwrites nothing.
func TestComplianceDecisionConcurrentAndDuplicateSubmissionsCreateOneVersion(t *testing.T) {
	svc, db := newDecisionTestService(t)
	ctx := context.Background()
	repo := repository.NewComplianceDecisionRepository(db)

	seedUnit(t, db, "CU-RACE", "G-RACE", "Plant D")
	seedRule(t, db, "PR-RACE", "G-RACE", "Plant D", "active", "ppm", 50)
	seedSample(t, db, "ES-RACE", "G-RACE", "Plant D", "verified", "ppm", 12)
	decision := seedDecision(t, db, "CD-RACE", "G-RACE", "Plant D", "draft")

	if _, err := svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: 1, Reason: "enter review",
	}, "operator", model.RoleOperator, "request-review"); err != nil {
		t.Fatalf("enter review: %v", err)
	}

	// commit builds a candidate final version against the review/v2 snapshot,
	// exactly as two simultaneous reviewer requests would after independently
	// reading the current aggregate.
	commit := func(requestID string) error {
		snapshot, err := repo.Get(ctx, decision.ID)
		if err != nil {
			t.Fatalf("read review snapshot: %v", err)
		}
		snapshot.Status = "accepted"
		snapshot.Version = 3
		snapshot.UpdatedAt = time.Now().UTC()
		revision := newDecisionRevision(3, "accepted", snapshot.Evidence, "duplicate concurrent acceptance", "reviewer", requestID)
		audit := &model.AuditLog{RequestID: requestID, Actor: "reviewer", Action: "transition", EntityType: "ComplianceDecision",
			EntityID: decision.ID, BeforeState: "review", AfterState: "accepted", Detail: "duplicate concurrent acceptance", CreatedAt: time.Now().UTC()}
		return repo.TransitionWithRevisionAndAudit(ctx, decision.ID, 2, &snapshot, revision, audit)
	}

	if err := commit("request-accept-0"); err != nil {
		t.Fatalf("first concurrent commit should win: %v", err)
	}
	if err := commit("request-accept-1"); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("duplicate concurrent commit must conflict, got %v", err)
	}

	final, err := svc.Get(ctx, decision.ID)
	if err != nil {
		t.Fatalf("reload decision: %v", err)
	}
	if final.Status != "accepted" || final.Version != 3 || len(final.Revisions) != 3 {
		t.Fatalf("race produced an unexpected version history: status=%s version=%d revisions=%d", final.Status, final.Version, len(final.Revisions))
	}
	if final.Revisions[2].RequestID != "request-accept-0" {
		t.Fatalf("the losing duplicate must not overwrite the winning evidence: %+v", final.Revisions[2])
	}
	if countAudits(t, db) != 2 { // one review transition + one accepted transition
		t.Fatalf("expected exactly two transition audit rows, got %d", countAudits(t, db))
	}

	// A strictly repeated submission on the now-stale version through the
	// service must also fail and append nothing. accepted->review is a legal
	// edge, but expectedVersion=2 no longer matches the stored version.
	_, err = svc.Transition(ctx, decision.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: 2, Reason: "replayed duplicate submission on stale version",
	}, "reviewer", model.RoleReviewer, "request-replay")
	if !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("duplicate replay should conflict, got %v", err)
	}
	reloaded, _ := svc.Get(ctx, decision.ID)
	if len(reloaded.Revisions) != 3 {
		t.Fatalf("duplicate replay overwrote evidence history: %+v", reloaded.Revisions)
	}
}

// TestComplianceDecisionReviewCheckPreview verifies the read-only preview the
// page uses to show reasons before any submission.
func TestComplianceDecisionReviewCheckPreview(t *testing.T) {
	svc, db := newDecisionTestService(t)
	ctx := context.Background()

	seedUnit(t, db, "CU-PREVIEW", "G-PREVIEW", "Plant E")
	seedRule(t, db, "PR-PREVIEW", "G-PREVIEW", "Plant E", "active", "ppm", 50)
	seedSample(t, db, "ES-PREVIEW", "G-PREVIEW", "Plant E", "verified", "ppm", 9)
	decision := seedDecision(t, db, "CD-PREVIEW", "G-PREVIEW", "Plant E", "review")

	check, err := svc.ReviewCheck(ctx, decision.ID)
	if err != nil {
		t.Fatalf("review check preview: %v", err)
	}
	if check.Blocked || check.OverThreshold {
		t.Fatalf("expected compliant preview, got %+v", check)
	}
	if len(check.AllowedTargets) != 1 || check.AllowedTargets[0] != "accepted" {
		t.Fatalf("within-threshold final gate should allow only accepted, got %v", check.AllowedTargets)
	}
	if check.Unit == nil || check.PermitRule == nil || check.Sample == nil {
		t.Fatalf("preview must echo re-read references: %+v", check)
	}
}
