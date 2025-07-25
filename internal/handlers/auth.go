package handlers

import (
	"net/http"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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
// @Success 200 {object} map[string]interface{} "注册成功"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Failure 409 {object} map[string]interface{} "用户名或邮箱已存在"
// @Router /api/v1/auth/register [post]
func Register(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	userRepo := repositories.NewUserRepository(db)
	authService := services.NewAuthService(userRepo, cfg)
	return func(c *gin.Context) {
		var req RegisterRequest
		if err := c.ShouldBind(&req); err != nil {
			// 参数绑定错误，使用通用错误响应
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
			return
		}

		user, err := authService.RegisterUser(req.Username, req.Password, req.Email)
		if err != nil {
			// 根据错误类型返回不同的状态码和业务码
			if err.Error() == "username already exists" {
				xerr.Error(c, http.StatusConflict, xerr.CodeUserAlreadyExists, err.Error())
				return
			}
			if err.Error() == "email already exists" {
				xerr.Error(c, http.StatusConflict, xerr.CodeEmailAlreadyExists, err.Error())
				return
			}
			// 其他内部服务器错误
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Failed to register user")
			return
		}

		xerr.Success(c, http.StatusOK, "User registered successfully", gin.H{
			"user_id":  user.ID,
			"username": user.Username,
			"email":    user.Email,
		})
	}
}

// @Summary 用户登录
// @Description 用户登录接口
// @Tags 用户认证
// @Accept json
// @Produce json
// @Param data body LoginRequest true "登录信息"
// @Success 200 {object} map[string]interface{} "登录成功，返回token"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Failure 401 {object} map[string]interface{} "用户名或密码错误"
// @Router /api/v1/auth/login [post]
func Login(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	userRepo := repositories.NewUserRepository(db)
	authService := services.NewAuthService(userRepo, cfg)
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
			return
		}

		tokenString, err := authService.LoginUser(req.Identifier, req.Password)
		if err != nil {
			if err.Error() == "user not found" {
				xerr.Error(c, http.StatusUnauthorized, xerr.CodeUserNotFound, "User not found")
				return
			}
			if err.Error() == "invalid credentials" {
				xerr.Error(c, http.StatusUnauthorized, xerr.CodeInvalidCredentials, "Invalid username or password")
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Failed to login")
			return
		}

		xerr.Success(c, http.StatusOK, "Login successful", gin.H{"token": tokenString})
	}
}

// @Summary 刷新Token
// @Description 刷新JWT Token
// @Tags 用户认证
// @Produce json
// @Success 200 {object} map[string]interface{} "刷新成功"
// @Router /api/v1/auth/refresh [post]
func RefreshToken(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "Refresh token endpoint - To be implemented", nil)
	}
}
