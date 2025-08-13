package handlers

import (
	"errors"
	"net/http"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers/response"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services/admin"
	"github.com/gin-gonic/gin"
)

// AuthHandler 结构体持有 AuthService 依赖
type AuthHandler struct {
	authService admin.AuthService
	cfg         *config.Config
}

// NewAuthHandler 创建 AuthHandler 实例的构造函数
func NewAuthHandler(authService admin.AuthService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		cfg:         cfg,
	}
}

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Password string `json:"password" binding:"required,min=6,max=255"`
	Email    string `json:"email" binding:"required,email"`
}

// LoginRequest 登录请求结构体
type LoginRequest struct {
	Identifier string `json:"identifier" binding:"required"` // 可以是用户名或邮箱
	Password   string `json:"password" binding:"required"`
}

// @Summary 用户注册
// @Description 用户注册接口
// @Tags 用户认证
// @Accept json
// @Produce json
// @Param data body RegisterRequest true "注册信息"
// @Success 200 {object} xerr.Response "注册成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 409 {object} xerr.Response "用户名或邮箱已存在"
// @Router /api/v1/auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBind(&req); err != nil {
		// 参数绑定错误，使用通用错误响应
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, err.Error())
		return
	}

	_, err := h.authService.RegisterUser(req.Username, req.Password, req.Email)
	if err != nil {
		// 根据错误类型返回不同的状态码和业务码
		if errors.Is(err, xerr.ErrUserAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.UserAlreadyExistsCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrEmailAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.EmailAlreadyExistsCode, err.Error())
			return
		}
		// 其他内部服务器错误
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "注册失败")
		return
	}

	response.Success(c, http.StatusOK, "注册成功", nil)
}

// @Summary 用户登录
// @Description 用户登录接口
// @Tags 用户认证
// @Accept json
// @Produce json
// @Param data body LoginRequest true "登录信息"
// @Success 200 {object} xerr.Response "登录成功，返回token"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 401 {object} xerr.Response "用户名或密码错误"
// @Router /api/v1/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, err.Error())
		return
	}

	token, err := h.authService.LoginUser(req.Identifier, req.Password)
	if err != nil {
		if errors.Is(err, xerr.ErrUserNotFound) {
			response.Error(c, http.StatusUnauthorized, xerr.UserNotFoundCode, "用户不存在")

			return
		}
		if errors.Is(err, xerr.ErrInvalidCredentials) {
			response.Error(c, http.StatusUnauthorized, xerr.InvalidCredentialsCode, "账户名或密码错误")
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "登陆失败")
		return
	}

	response.Success(c, http.StatusOK, "登录成功", gin.H{"token": token})
}

// @Summary 刷新Token
// @Description 刷新JWT Token
// @Tags 用户认证
// @Produce json
// @Success 200 {object} xerr.Response "刷新成功"
// @Router /api/v1/auth/refresh [post]
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	//TODO
	response.Success(c, http.StatusOK, "Refresh token endpoint - To be implemented", nil)
}
