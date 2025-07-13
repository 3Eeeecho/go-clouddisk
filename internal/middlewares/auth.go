package middlewares

import (
	"errors"
	"net/http"
	"strings"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func AuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 从请求头获取 Token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			xerr.AbortWithError(c, http.StatusUnauthorized, xerr.CodeUnauthorized, "Authorization header is required")
			return
		}

		// Token 格式通常是 "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			xerr.AbortWithError(c, http.StatusUnauthorized, xerr.CodeUnauthorized, "Invalid Authorization header format")
			return
		}
		tokenString := parts[1]

		// 2. 解析和验证 Token
		claims := &utils.Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(cfg.JWT.SecretKey), nil
		})

		if err != nil {
			xerr.AbortWithError(c, http.StatusUnauthorized, xerr.CodeUnauthorized, "Invalid or malformed token: "+err.Error()) // 统一返回错误
			return
		}

		if !token.Valid {
			xerr.AbortWithError(c, http.StatusUnauthorized, xerr.CodeUnauthorized, "Invalid token")
			return
		}

		// 3. 将用户信息存储到 Gin Context 中，以便后续 Handler 使用
		c.Set("userID", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("email", claims.Email)

		c.Next() // Token 有效，继续处理请求
	}
}
