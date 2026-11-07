package handles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestIsDirectLoopback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       bool
	}{
		{
			name:       "Direct IPv4 loopback without headers",
			remoteAddr: "127.0.0.1:54321",
			headers:    nil,
			want:       true,
		},
		{
			name:       "Direct IPv6 loopback without headers",
			remoteAddr: "[::1]:54321",
			headers:    nil,
			want:       true,
		},
		{
			name:       "Public IP with spoofed X-Forwarded-For: 127.0.0.1",
			remoteAddr: "198.51.100.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			want:       false,
		},
		{
			name:       "Public IP without headers",
			remoteAddr: "198.51.100.1:54321",
			headers:    nil,
			want:       false,
		},
		{
			name:       "Private LAN IP 192.168.1.50 without headers",
			remoteAddr: "192.168.1.50:54321",
			headers:    nil,
			want:       false,
		},
		{
			name:       "Private LAN IP 10.0.0.2 without headers",
			remoteAddr: "10.0.0.2:54321",
			headers:    nil,
			want:       false,
		},
		{
			name:       "Loopback connection but behind reverse proxy (X-Forwarded-For present)",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			want:       false,
		},
		{
			name:       "Loopback connection but behind reverse proxy (X-Real-IP present)",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Real-IP": "127.0.0.1"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest(http.MethodPost, "/api/public/init/setup", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			c.Request = req

			got := isDirectLoopback(c)
			if got != tt.want {
				t.Errorf("isDirectLoopback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInitSetupTokenValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Configure conf.Conf first before db.Init
	conf.Conf = &conf.Config{
		SetupToken: "secret-setup-token-12345",
	}
	conf.Conf.Database.Type = "sqlite3"

	// Configure in-memory SQLite database
	d, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}
	db.Init(d)

	tests := []struct {
		name       string
		remoteAddr string
		token      string
		tokenWhere string // "auth_header", "custom_header", "query", "body", "none"
		body       string
		wantCode   int // Expected business response code (403, 200, 400)
	}{
		{
			name:       "Direct loopback without token must be rejected with 403",
			remoteAddr: "127.0.0.1:12345",
			token:      "",
			tokenWhere: "none",
			body:       `{"username":"admin","password":"password123"}`,
			wantCode:   403,
		},
		{
			name:       "Reverse proxy without token must be rejected with 403",
			remoteAddr: "127.0.0.1:12345",
			token:      "",
			tokenWhere: "none",
			body:       `{"username":"admin","password":"password123"}`,
			wantCode:   403,
		},
		{
			name:       "Wrong token via Authorization header must be rejected with 403",
			remoteAddr: "127.0.0.1:12345",
			token:      "wrong-token",
			tokenWhere: "auth_header",
			body:       `{"username":"admin","password":"password123"}`,
			wantCode:   403,
		},
		{
			name:       "Valid token via Authorization header succeeds and initializes system",
			remoteAddr: "127.0.0.1:12345",
			token:      "secret-setup-token-12345",
			tokenWhere: "auth_header",
			body:       `{"username":"admin","password":"password123"}`,
			wantCode:   200,
		},
		{
			name:       "Subsequent init attempt after already initialized must return 400",
			remoteAddr: "127.0.0.1:12345",
			token:      "secret-setup-token-12345",
			tokenWhere: "auth_header",
			body:       `{"username":"admin2","password":"password456"}`,
			wantCode:   400,
		},
		{
			name:       "Valid token via X-Setup-Token header succeeds",
			remoteAddr: "198.51.100.1:12345",
			token:      "secret-setup-token-12345",
			tokenWhere: "custom_header",
			body:       `{"username":"admin","password":"password123"}`,
			wantCode:   200,
		},
		{
			name:       "Valid token via query param succeeds",
			remoteAddr: "198.51.100.1:12345",
			token:      "secret-setup-token-12345",
			tokenWhere: "query",
			body:       `{"username":"admin","password":"password123"}`,
			wantCode:   200,
		},
		{
			name:       "Valid token via JSON body succeeds",
			remoteAddr: "198.51.100.1:12345",
			token:      "secret-setup-token-12345",
			tokenWhere: "body",
			body:       `{"username":"admin","password":"password123","token":"secret-setup-token-12345"}`,
			wantCode:   200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantCode == 200 {
				d.Exec("DELETE FROM users")
				op.DelAdminCache()
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			url := "/api/public/init/setup"
			if tt.tokenWhere == "query" {
				url += "?token=" + tt.token
			}

			req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = tt.remoteAddr

			switch tt.tokenWhere {
			case "auth_header":
				req.Header.Set("Authorization", tt.token)
			case "custom_header":
				req.Header.Set("X-Setup-Token", tt.token)
			}
			c.Request = req

			InitSetup(c)

			var resp struct {
				Code    int         `json:"code"`
				Message string      `json:"message"`
				Data    interface{} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)

			if resp.Code != tt.wantCode {
				t.Fatalf("expected business code %d, got %d (body: %s)", tt.wantCode, resp.Code, w.Body.String())
			}
		})
	}
}
