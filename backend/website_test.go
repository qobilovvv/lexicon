package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func webRequest(h http.Handler, method, path string, fields url.Values, cookie *http.Cookie, hx bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(fields.Encode()))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if hx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestWebsiteAuthenticationAndForms(t *testing.T) {
	app, _ := testApplication(t)
	h := app.website()
	if w := webRequest(h, "GET", "/", nil, nil, false); w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal(w.Code, w.Header())
	}
	for _, path := range []string{"/login", "/register", "/static/htmx.min.js", "/static/style.css"} {
		if w := webRequest(h, "GET", path, nil, nil, false); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	fields := url.Values{"username": {"alice"}, "full_name": {"Alice"}, "email": {"alice@example.com"}, "password": {"correct horse notebook"}}
	w := webRequest(h, "POST", "/actions/register", fields, nil, true)
	if w.Code != 200 || w.Header().Get("HX-Location") == "" || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	cookie := w.Result().Cookies()[0]
	for _, path := range []string{"/", "/?q=rain", "/?folder=2", "/?sort=az", "/words/new", "/words/9", "/words/9/edit", "/words/9/delete", "/folders/new", "/news", "/news/new", "/profile"} {
		w = webRequest(h, "GET", path, nil, cookie, true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `id="app"`) || strings.Contains(w.Body.String(), "ZgotmplZ") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w = webRequest(h, "POST", "/actions/word", url.Values{"title": {""}, "folderId": {"1"}, "description": {"Keep my draft"}}, cookie, true)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep my draft") || !strings.Contains(w.Body.String(), `role="alert"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = webRequest(h, "POST", "/actions/word", url.Values{"title": {"<script>alert(1)</script>"}, "folderId": {"1"}, "description": {"## Meaning\n\n<script>alert(2)</script>"}}, cookie, true)
	if w.Header().Get("HX-Location") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	d := readWorkspace(t, h, cookie)
	id := d.Notes[0].ID
	w = webRequest(h, "GET", fmt.Sprintf("/words/%d", id), nil, cookie, true)
	if strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), "<h2>Meaning</h2>") {
		t.Fatal(w.Body.String())
	}
	w = webRequest(h, "GET", "/?q=petrichor&folder=2", nil, cookie, true)
	if !strings.Contains(w.Body.String(), ">Petrichor</a>") || strings.Contains(w.Body.String(), ">Serendipity</a>") {
		t.Fatal("search must span all folders")
	}
	w = webRequest(h, "POST", "/actions/word-edit", url.Values{"id": {fmt.Sprint(id)}, "title": {"Updated"}, "folderId": {"2"}, "description": {"Changed"}}, cookie, false)
	if w.Code != 303 || w.Header().Get("Location") != fmt.Sprintf("/words/%d", id) {
		t.Fatal(w.Code, w.Header())
	}
	w = webRequest(h, "POST", "/actions/word-delete", url.Values{"id": {fmt.Sprint(id)}}, cookie, false)
	if w.Code != 303 || len(readWorkspace(t, h, cookie).Notes) != 6 {
		t.Fatal("delete failed", w.Body.String())
	}
	w = webRequest(h, "POST", "/actions/folder", url.Values{"name": {"Spanish"}}, cookie, false)
	if w.Code != 303 || len(readWorkspace(t, h, cookie).Folders) != 4 {
		t.Fatal("folder failed", w.Body.String())
	}
	w = webRequest(h, "POST", "/actions/logout", nil, cookie, true)
	if len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	w = webRequest(h, "GET", "/", nil, cookie, true)
	if !strings.Contains(w.Header().Get("HX-Location"), "/login") {
		t.Fatal("expired session did not redirect")
	}
	w = webRequest(h, "POST", "/actions/login", url.Values{"identifier": {"alice"}, "password": {"correct horse notebook"}}, nil, false)
	if w.Code != 303 || len(w.Result().Cookies()) != 1 {
		t.Fatal("form login failed", w.Code, w.Body.String())
	}
}

func TestWebsiteIsolationOriginsAndNews(t *testing.T) {
	app, h := testApplication(t)
	owner, _ := registerAccount(t, h, "alice")
	other, _ := registerAccount(t, h, "bobby")
	web := app.website()
	w := webRequest(web, "GET", "/words/9", nil, other, true)
	if w.Code != 404 {
		t.Fatal("other user's word exposed")
	}
	for _, bad := range []string{"https://evil.example", "null"} {
		r := httptest.NewRequest("POST", "/actions/folder", strings.NewReader("name=Injected"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", bad)
		r.AddCookie(owner)
		w := httptest.NewRecorder()
		web.ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), "Request origin not allowed") {
			t.Fatal("origin accepted", w.Code, w.Body.String())
		}
	}
	if len(readWorkspace(t, h, owner).Folders) != 3 {
		t.Fatal("CSRF changed data")
	}
	for _, cookie := range []*http.Cookie{owner, other} {
		w := webRequest(web, "POST", "/actions/news", url.Values{"title": {"Update"}, "body": {"# Hello"}}, cookie, true)
		if cookie == owner && w.Header().Get("HX-Location") == "" {
			t.Fatal(w.Code, w.Body.String())
		}
		if cookie == other && w.Code != 403 {
			t.Fatal("non-owner posted", w.Code, w.Body.String())
		}
	}
	w = webRequest(web, "GET", "/news", nil, other, false)
	if !strings.Contains(w.Body.String(), "Update") || strings.Contains(w.Body.String(), "+ New post") {
		t.Fatal("news permissions wrong")
	}
	w = webRequest(web, "POST", "/actions/news-edit", url.Values{"id": {"1"}, "title": {"Edited"}, "body": {"Body"}}, owner, false)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = webRequest(web, "POST", "/actions/news-delete", url.Values{"id": {"1"}}, owner, false)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWebsiteProfileUpload(t *testing.T) {
	app, h := testApplication(t)
	cookie, _ := registerAccount(t, h, "alice")
	web := app.website()
	var body, picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1200, 1400)))
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"full_name": "Alice Updated", "username": "alice", "email": "alice@example.com", "bio": "My words"} {
		writer.WriteField(key, value)
	}
	file, _ := writer.CreateFormFile("avatar_file", "avatar.png")
	file.Write(picture.Bytes())
	writer.Close()
	r := httptest.NewRequest("POST", "/actions/profile", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	web.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send(t, h, "GET", "/api/profile", nil, cookie)
	var result struct{ User profile }
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.User.FullName != "Alice Updated" || result.User.Avatar == "" {
		t.Fatal("profile not updated")
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.SplitN(result.User.Avatar, ",", 2)[1])
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != 256 || config.Height != 256 {
		t.Fatal("avatar not resized", config, err)
	}
	w = webRequest(web, "GET", "/profile", nil, cookie, false)
	if strings.Contains(w.Body.String(), "ZgotmplZ") {
		t.Fatal("avatar blocked by template")
	}
	fields := url.Values{"full_name": {"Alice"}, "username": {"alice"}, "email": {"changed@example.com"}, "bio": {"Draft bio"}}
	w = webRequest(web, "POST", "/actions/profile", fields, cookie, true)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Draft bio") {
		t.Fatal("profile errors lose fields")
	}
	w = webRequest(web, "POST", "/actions/profile", url.Values{"full_name": {"Alice"}, "username": {"alice"}, "email": {"alice@example.com"}, "remove_avatar": {"on"}}, cookie, false)
	if w.Code != 303 {
		t.Fatal("remove avatar failed", w.Code, w.Body.String())
	}
}
