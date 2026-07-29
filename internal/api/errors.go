package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/AkkunYo/SyncHub/internal/mapping"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/platform/cliproxyapi"
	"github.com/AkkunYo/SyncHub/internal/platform/newapi"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
	"github.com/gin-gonic/gin"
)

var safeErrorMessages = map[string]string{
	"invalid_request":     "请求参数无效",
	"target_not_found":    "目标实例不存在",
	"upstream_not_found":  "上游实例不存在",
	"asset_not_found":     "资产不存在",
	"channel_not_found":   "目标渠道不存在",
	"resource_in_use":     "资源仍有关联映射",
	"incompatible_target": "目标与资产不兼容",
	"needs_reconcile":     "远端状态需要重新校验",
	"secret_unavailable":  "资产秘密不可用",
	"upstream_failure":    "平台请求失败",
	"upstream_timeout":    "平台请求超时",
	"internal_error":      "内部错误",
	"group_required":      "必须选择上游分组",
	"group_unknown":       "上游分组不可用",
}

type errorDescriptor struct {
	status int
	code   string
}

var (
	invalidRequestError = errorDescriptor{status: http.StatusBadRequest, code: "invalid_request"}
	internalError       = errorDescriptor{status: http.StatusInternalServerError, code: "internal_error"}
	upstreamFailure     = errorDescriptor{status: http.StatusBadGateway, code: "upstream_failure"}
)

func respondDependencyError(c *gin.Context, err error, fallback errorDescriptor) {
	descriptor := classifyError(err, fallback)
	writeFailure(c, descriptor.status, descriptor.code)
}

func classifyError(err error, fallback errorDescriptor) errorDescriptor {
	switch {
	case err == nil:
		return fallback
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.Is(err, ErrUpstreamTimeout):
		return errorDescriptor{status: http.StatusGatewayTimeout, code: "upstream_timeout"}
	case errors.Is(err, ErrTargetNotFound):
		return errorDescriptor{status: http.StatusNotFound, code: "target_not_found"}
	case errors.Is(err, ErrUpstreamNotFound):
		return errorDescriptor{status: http.StatusNotFound, code: "upstream_not_found"}
	case errors.Is(err, ErrAssetNotFound):
		return errorDescriptor{status: http.StatusNotFound, code: "asset_not_found"}
	case errors.Is(err, ErrChannelNotFound), errors.Is(err, mapping.ErrMappingNotFound),
		errors.Is(err, newapi.ErrChannelNotFound), errors.Is(err, cliproxyapi.ErrChannelNotFound):
		return errorDescriptor{status: http.StatusNotFound, code: "channel_not_found"}
	case errors.Is(err, ErrResourceInUse):
		return errorDescriptor{status: http.StatusConflict, code: "resource_in_use"}
	case errors.Is(err, ErrIncompatibleTarget), errors.Is(err, platform.ErrIncompatibleTarget):
		return errorDescriptor{status: http.StatusConflict, code: "incompatible_target"}
	case errors.Is(err, ErrNeedsReconcile), errors.Is(err, syncservice.ErrMappingPersist):
		return errorDescriptor{status: http.StatusConflict, code: "needs_reconcile"}
	case errors.Is(err, ErrSecretUnavailable), errors.Is(err, platform.ErrSecretUnavailable),
		errors.Is(err, platform.ErrSecretGrantRequired), errors.Is(err, platform.ErrAssetDisabled),
		errors.Is(err, syncservice.ErrSecretResolve):
		return errorDescriptor{status: http.StatusUnprocessableEntity, code: "secret_unavailable"}
	case errors.Is(err, ErrUpstreamFailure):
		return upstreamFailure
	default:
		return fallback
	}
}
