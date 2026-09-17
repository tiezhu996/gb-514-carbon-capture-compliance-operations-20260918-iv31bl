package service

import (
	"errors"
	"strings"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/model"
)

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
	ErrReviewerRequired  = errors.New("reviewer or admin role is required for this decision")
	ErrDecisionLocked    = errors.New("compliance decision fields are locked after review begins")
)

// ReviewGateError 表示复核闭环在重读关联装置、生效规则与已核验样本后拒绝了本次
// 提交。它不改变任何状态、不产生版本，只携带当前状态与必须展示给复核人的原因。
type ReviewGateError struct {
	Gate model.ReviewGate
}

func (e *ReviewGateError) Error() string {
	if len(e.Gate.Reasons) == 0 {
		return "复核核验未通过，决定保持原状态"
	}
	return "复核核验未通过，决定保持原状态：" + strings.Join(e.Gate.Reasons, "；")
}

// NewReviewGateError 用最新核验快照构造闭环拒绝错误。
func NewReviewGateError(gate model.ReviewGate) *ReviewGateError {
	return &ReviewGateError{Gate: gate}
}
