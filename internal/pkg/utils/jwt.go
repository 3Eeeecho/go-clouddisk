package utils

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// GenerateToken 用于生成 JWT Token
// user ID, username, email: 用户的基本信息
// secretKey: 用于签名的密钥
// expiresIn: Token 的过期时间（分钟）
// issuer: Token 的签发者
func GenerateToken(userID uint64, username, email, secretKey, issuer string, expiresIn time.Duration) (string, error) {
	expirationTime := time.Now().Add(expiresIn * time.Minute)
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Email:    email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    issuer,
			Subject:   fmt.Sprintf("%d", userID), // Subject 通常是 token 的主体
			ID:        fmt.Sprintf("%d", userID), // ID 是 token 的唯一标识符
			Audience:  []string{"users"},         // 接收者
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}
