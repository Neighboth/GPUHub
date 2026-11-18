package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour
)

type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"sub"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// Service issues and verifies JWTs. Secret from GPUHUB_JWT_SECRET (or master key).
type Service struct{ secret []byte }

func NewService(secret string) *Service {
	sum := sha256.Sum256([]byte("gpuhub-jwt:" + secret))
	return &Service{secret: sum[:]}
}

func (s *Service) IssueAccess(userID int64, username, role string) (string, error) {
	claims := Claims{
		UserID: userID, Username: username, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(AccessTokenTTL)),
			Issuer:    "gpuhub",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

func (s *Service) ParseAccess(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// NewRefreshToken returns an opaque random token and its sha256 hash for storage.
func NewRefreshToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func CheckPassword(hash, pw string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}

var ErrInvalidCredentials = errors.New("invalid username or password")

// dummyCompare keeps timing uniform when the user does not exist.
func dummyCompare(pw string) {
	_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$abcdefghijklmnopqrstuuVZpRj8LjY1iF8d0R6iF0ZV9e0l0u0i0a0"), []byte(pw))
}

func CompareOrDummy(hash, pw string) error {
	if hash == "" {
		dummyCompare(pw)
		return ErrInvalidCredentials
	}
	return CheckPassword(hash, pw)
}

var _ = context.Background
