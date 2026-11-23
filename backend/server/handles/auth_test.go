package handles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLoginHash2FAChallengeDoesNotConsumeRetryQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conf.Conf = &conf.Config{}
	conf.Conf.Database.Type = "sqlite3"

	d, err := gorm.Open(sqlite.Open("file:auth_2fa_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}
	db.Init(d)
	d.Exec("DELETE FROM users")
	op.DelAdminCache()

	admin := &model.User{
		Username:   "admin",
		Role:       model.ADMIN,
		BasePath:   "/",
		Authn:      "[]",
		Permission: 0x71FF,
		OtpSecret:  "JBSWY3DPEHPK3PXP",
	}
	admin.SetPassword("password123")
	if err := op.CreateUser(admin); err != nil {
		t.Fatalf("create admin failed: %v", err)
	}
	t.Cleanup(func() {
		d.Exec("DELETE FROM users")
		op.DelAdminCache()
		op.DelUserCache("admin")
		model.LoginCache.Del("203.0.113.9")
	})

	staticHash := model.StaticHash("password123")
	sendLogin := func(otp string) int {
		payload, _ := json.Marshal(LoginReq{
			Username: "admin",
			Password: staticHash,
			OtpCode:  otp,
		})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login/hash", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.9:12345"
		c.Request = req
		LoginHash(c)

		var resp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response failed: %v (body=%s)", err, w.Body.String())
		}
		return resp.Code
	}

	model.LoginCache.Del("203.0.113.9")

	// Empty OTP after valid password returns 402 challenge without incrementing failure count.
	if code := sendLogin(""); code != 402 {
		t.Fatalf("expected 402 for 2FA challenge, got %d", code)
	}
	if count, ok := model.LoginCache.Get("203.0.113.9"); ok && count != 0 {
		t.Fatalf("expected 0 failed attempts after empty OTP challenge, got %d", count)
	}

	// Non-empty invalid OTP must increment failure count.
	if code := sendLogin("000000"); code != 402 {
		t.Fatalf("expected 402 for wrong OTP, got %d", code)
	}
	if count, ok := model.LoginCache.Get("203.0.113.9"); !ok || count != 1 {
		t.Fatalf("expected 1 failed attempt after wrong OTP, got ok=%v count=%d", ok, count)
	}
}
