package handles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAnonymousPrivateReadsRequireLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"list", FsListSplit}, {"get", FsGetSplit},
		{"archive/meta", FsArchiveMetaSplit}, {"archive/list", FsArchiveListSplit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.POST("/test", tc.handler)
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"path":"/private"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			var result struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != 401 {
				t.Fatalf("got %d: %s", result.Code, rec.Body.String())
			}
		})
	}
}
