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

// Register 处理用户注册请求
// 这是一个闭包，用于注入依赖 (DB 和 Config)
func Register(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	userRepo := repositories.NewUserRepository(db)
	authService := services.NewAuthService(userRepo)
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

// Login 占位函数
func Login(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "Login endpoint - To be implemented", nil)
	}
}

// RefreshToken 占位函数
func RefreshToken(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "Refresh token endpoint - To be implemented", nil)
	}
}

// GetUserInfo 占位函数
func GetUserInfo() gin.HandlerFunc {
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "Get user info endpoint - To be implemented", nil)
	}
}
