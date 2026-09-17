package service

import "errors"

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
	ErrReviewerRequired  = errors.New("reviewer or admin role is required for this decision")
	ErrDecisionLocked    = errors.New("compliance decision fields are locked after review begins")
	// ErrReviewCheckFailed keeps the decision in its current state because the
	// re-read permit rule / verified sample / device context does not support
	// the requested move. ReviewCheck.Reasons carries the page-facing detail.
	ErrReviewCheckFailed = errors.New("compliance review gate rejected the transition")
)
