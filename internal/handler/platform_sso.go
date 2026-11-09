package handler

import (
	stderrors "errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *AuthHandler) PlatformSSOConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.configInfo == nil || h.configInfo.PlatformSSO == nil || !h.configInfo.PlatformSSO.Enabled {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	cfg := h.configInfo.PlatformSSO
	c.JSON(http.StatusOK, gin.H{"enabled": true, "sso_only": cfg.OnlyLogin, "client_id": cfg.ClientID, "parent_origins": cfg.ParentOrigins})
}

func (h *AuthHandler) PlatformSSOExchange(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.configInfo == nil || h.configInfo.PlatformSSO == nil || !h.configInfo.PlatformSSO.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "平台单点登录尚未配置"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 70000)
	var req types.PlatformSSOLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的登录票据请求"})
		return
	}
	resp, err := h.userService.LoginWithPlatformSSO(c.Request.Context(), &req, h.resolveDefaultTenantMode(c.Request.Context()))
	if err != nil {
		status, message := http.StatusConflict, "账号关联失败，请联系管理员确认本地账号绑定配置"
		if stderrors.Is(err, types.ErrPlatformSSOInvalid) {
			status, message = http.StatusUnauthorized, "平台登录票据无效、已过期或不属于本应用"
		}
		if stderrors.Is(err, types.ErrPlatformSSOUnavailable) {
			status, message = http.StatusServiceUnavailable, "平台认证服务暂时不可用，请稍后重试"
		}
		c.JSON(status, gin.H{"success": false, "message": message})
		return
	}
	c.JSON(http.StatusOK, resp)
}
