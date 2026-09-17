package handler

import (
	"errors"
	"net/http"

	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/repository"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/service"
	"github.com/blueship581/carbon-capture-compliance-operations/backend/internal/util"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func handleError(c *gin.Context, err error) {
	var gateErr *service.ReviewGateError
	switch {
	case errors.As(err, &gateErr):
		// 复核闭环拒绝：决定保持原状态，返回最新核验视图与原因供页面展示。
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, util.Envelope{
			Error:   "review_gate",
			Message: gateErr.Error(),
			Data:    gin.H{"reviewGate": gateErr.Gate},
		})
	case errors.Is(err, gorm.ErrRecordNotFound):
		util.Fail(c, http.StatusNotFound, "not_found", "record was not found")
	case errors.Is(err, repository.ErrVersionConflict):
		util.Fail(c, http.StatusConflict, "version_conflict", "record changed; refresh and retry")
	case errors.Is(err, service.ErrInvalidTransition), errors.Is(err, service.ErrInvalidInput),
		errors.Is(err, service.ErrReviewerRequired), errors.Is(err, service.ErrDecisionLocked):
		util.Fail(c, http.StatusUnprocessableEntity, "business_rule", err.Error())
	default:
		_ = c.Error(err)
		util.Fail(c, http.StatusInternalServerError, "internal_error", "request could not be completed")
	}
}
