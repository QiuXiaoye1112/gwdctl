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

func TestInitSetup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	conf.Conf = &conf.Config{}
	conf.Conf.Database.Type = "sqlite3"

	d, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}
	db.Init(d)

	tests := []struct {
		name       string
		remoteAddr string
		body       string
		resetDB    bool
		wantCode   int
	}{
		{
			name:       "Short password must be rejected with 400",
			remoteAddr: "127.0.0.1:12345",
			body:       `{"username":"admin","password":"123"}`,
			resetDB:    true,
			wantCode:   400,
		},
		{
			name:       "Init without setup token succeeds",
			remoteAddr: "198.51.100.1:12345",
			body:       `{"username":"admin","password":"password123"}`,
			resetDB:    true,
			wantCode:   200,
		},
		{
			name:       "Subsequent init attempt after already initialized must return 400",
			remoteAddr: "127.0.0.1:12345",
			body:       `{"username":"admin2","password":"password456"}`,
			resetDB:    false,
			wantCode:   400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.resetDB {
				d.Exec("DELETE FROM users")
				op.DelAdminCache()
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			req, _ := http.NewRequest(http.MethodPost, "/api/public/init/setup", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = tt.remoteAddr
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
