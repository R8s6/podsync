package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mxpv/podsync/pkg/fs"
)

const testAdminToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func adminFixture(t *testing.T, enabled bool) (*Server, *int) {
	t.Helper()
	storage, err := fs.NewLocal(t.TempDir(), false, false)
	require.NoError(t, err)
	for _, name := range []string{"index.html", "show.xml", "podsync.opml", "show/audio.mp3"} {
		_, err := storage.Create(context.Background(), name, strings.NewReader("public content"))
		require.NoError(t, err)
	}
	s := New(Config{Path: "podcasts", WebUIEnabled: true}, storage, nil)
	cfg := AdminConfig{Enabled: enabled, Token: testAdminToken, Origin: "https://podsync.test"}
	cfg.Defaults.ApplyDefaults()
	added := new(int)
	require.NoError(t, s.EnableAdmin(cfg, func(context.Context, string) (string, error) {
		*added++
		return "web_123", nil
	}))
	return s, added
}

func adminCall(s *Server, method, action, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://podsync.test/podcasts/api/admin/"+action, strings.NewReader(body))
	r.Header.Set("Origin", "https://podsync.test")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, r)
	return w
}

func signIn(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := adminCall(s, "POST", "login", `{"token":"`+testAdminToken+`"}`, nil, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), testAdminToken)
	var response map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	require.True(t, cookie.HttpOnly && cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, "/podcasts/api/admin/", cookie.Path)
	require.NotEmpty(t, response["csrf"])
	return cookie, response["csrf"]
}

func TestAdminDisabledAndPublicAccess(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s, added := adminFixture(t, enabled)
		if !enabled {
			for _, action := range []string{"capabilities", "session", "login", "feeds", "logout"} {
				for _, method := range []string{"GET", "POST"} {
					w := adminCall(s, method, action, `{}`, nil, "")
					require.Equal(t, http.StatusNotFound, w.Code)
				}
			}
		}
		// Podcast clients never need credentials, even with administration enabled.
		for _, path := range []string{"/", "/show.xml", "/podsync.opml", "/show/audio.mp3"} {
			for _, method := range []string{"GET", "HEAD"} {
				r := httptest.NewRequest(method, "http://lan-address/podcasts"+path, nil)
				r.Header.Set("Authorization", "Bearer invalid")
				r.Header.Set("Cookie", "__Secure-podsync_admin=expired")
				w := httptest.NewRecorder()
				s.Handler.ServeHTTP(w, r)
				require.Equal(t, http.StatusOK, w.Code, path)
				require.Empty(t, w.Header().Get("WWW-Authenticate"))
				require.Empty(t, w.Header().Get("Set-Cookie"))
			}
		}
		require.Zero(t, *added)
	}
}

func TestAdminAuthenticationAndCSRF(t *testing.T) {
	s, added := adminFixture(t, true)
	require.Equal(t, 200, adminCall(s, "GET", "capabilities", "", nil, "").Code)
	require.Equal(t, 401, adminCall(s, "GET", "session", "", nil, "").Code)
	require.Equal(t, 401, adminCall(s, "POST", "feeds", `{"url":"https://www.youtube.com/@example"}`, nil, "").Code)
	require.Equal(t, 401, adminCall(s, "POST", "login", `{"token":"wrong"}`, nil, "").Code)
	cookie, csrf := signIn(t, s)
	require.Equal(t, 200, adminCall(s, "GET", "session", "", cookie, "").Code)
	for _, token := range []string{"", "incorrect"} {
		require.Equal(t, 403, adminCall(s, "POST", "feeds", `{"url":"x"}`, cookie, token).Code)
	}
	for _, action := range []string{"login", "feeds", "logout"} {
		for _, origin := range []string{"", "null", "https://evil.test", "http://podsync.test", "https://podsync.test.evil.test"} {
			r := httptest.NewRequest("POST", "https://podsync.test/podcasts/api/admin/"+action, strings.NewReader(`{}`))
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", csrf)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			s.Handler.ServeHTTP(w, r)
			require.Equal(t, 403, w.Code, origin)
		}
	}
	r := httptest.NewRequest("POST", "https://untrusted.test/podcasts/api/admin/feeds", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://podsync.test")
	r.Header.Set("X-Forwarded-Host", "podsync.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, r)
	require.Equal(t, 403, w.Code)
	require.Zero(t, *added)

	w = adminCall(s, "POST", "feeds", `{"url":"https://www.youtube.com/@example"}`, cookie, csrf)
	require.Equal(t, 201, w.Code)
	require.Equal(t, 1, *added)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, 204, adminCall(s, "POST", "logout", `{}`, cookie, csrf).Code)
	require.Equal(t, 401, adminCall(s, "POST", "feeds", `{"url":"x"}`, cookie, csrf).Code)
}

func TestAdminRequestLimitsAndExpiry(t *testing.T) {
	s, added := adminFixture(t, true)
	cookie, csrf := signIn(t, s)
	for _, body := range []string{`{`, `{} {}`, `{"url":"x","youtube_dl_args":["--exec","command"]}`, `{"url":"` + strings.Repeat("x", 4096) + `"}`} {
		require.Equal(t, 400, adminCall(s, "POST", "feeds", body, cookie, csrf).Code)
	}
	require.Equal(t, 405, adminCall(s, "GET", "feeds", "", cookie, csrf).Code)
	require.Zero(t, *added)
	r := httptest.NewRequest("POST", "https://podsync.test/podcasts/api/admin/login", strings.NewReader("token=x"))
	r.Header.Set("Origin", "https://podsync.test")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, r)
	require.Equal(t, 415, w.Code)

	synctest.Test(t, func(t *testing.T) {
		s, _ := adminFixture(t, true)
		cookie, csrf := signIn(t, s)
		time.Sleep(8*time.Hour + time.Second)
		require.Equal(t, 401, adminCall(s, "POST", "feeds", `{"url":"x"}`, cookie, csrf).Code)
		for i := 0; i < 10; i++ {
			require.Equal(t, 401, adminCall(s, "POST", "login", `{"token":"wrong"}`, nil, "").Code)
		}
		require.Equal(t, 429, adminCall(s, "POST", "login", `{"token":"`+testAdminToken+`"}`, nil, "").Code)
		time.Sleep(time.Minute)
		cookie, csrf = signIn(t, s)
		for i := 0; i < 20; i++ {
			require.Equal(t, 201, adminCall(s, "POST", "feeds", `{"url":"x"}`, cookie, csrf).Code)
		}
		require.Equal(t, 429, adminCall(s, "POST", "feeds", `{"url":"x"}`, cookie, csrf).Code)
	})
}
