package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed web
var webFiles embed.FS

var pages = template.Must(template.New("page.html").Funcs(template.FuncMap{
	"date":     func(n Note) string { return n.CreatedAt.Format("Jan 2, 2006") },
	"markdown": renderDocument,
	"avatarURL": func(value string) template.URL {
		prefix, encoded, ok := strings.Cut(value, ",")
		if !ok || prefix != "data:image/jpeg;base64" {
			return ""
		}
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return ""
		}
		return template.URL(value)
	},
}).ParseFS(webFiles, "web/templates/*.html"))

type websiteView struct {
	User                                    profile
	Owner                                   bool
	Page, Title, Error, Query, Sort, Action string
	FolderID                                int
	Folders                                 []Folder
	Notes                                   []Note
	Total                                   int
	Counts                                  map[int]int
	Note                                    Note
	Posts                                   []announcement
	Post                                    announcement
	Values                                  url.Values
}

func (v websiteView) Value(key string) string { return v.Values.Get(key) }
func (v websiteView) Folder(id int) Folder {
	for _, f := range v.Folders {
		if f.ID == id {
			return f
		}
	}
	return Folder{Name: "Unknown folder", Color: "blue"}
}
func (v websiteView) ListURL(order string) string {
	q := url.Values{}
	if v.Query != "" {
		q.Set("q", v.Query)
	}
	if v.FolderID != 0 {
		q.Set("folder", strconv.Itoa(v.FolderID))
	}
	if order != "" {
		q.Set("sort", order)
	}
	return "/?" + q.Encode()
}

func (a *application) website() http.Handler {
	api := a.routes()
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFiles, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.Handle("/api/", api)
	mux.HandleFunc("POST /actions/{action}", func(w http.ResponseWriter, r *http.Request) { a.websiteAction(w, r, api) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", 405)
			return
		}
		a.websitePage(w, r, "", nil, 200)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}

func (a *application) websitePage(w http.ResponseWriter, r *http.Request, message string, values url.Values, status int) {
	v := websiteView{Page: strings.Trim(r.URL.Path, "/"), Title: "All words", Error: message, Values: values, Counts: map[int]int{}}
	if v.Values == nil {
		v.Values = url.Values{}
	}
	a.mu.Lock()
	user, signedIn := a.userLocked(r)
	workspace := a.workspaces[user.Profile.ID]
	a.mu.Unlock()
	if !signedIn && v.Page != "login" && v.Page != "register" {
		websiteRedirect(w, r, "/login")
		return
	}
	if signedIn && (v.Page == "login" || v.Page == "register") {
		websiteRedirect(w, r, "/")
		return
	}
	v.User, v.Owner = user.Profile, user.OwnsLegacy
	if signedIn {
		workspace.mu.Lock()
		v.Folders = append([]Folder{}, workspace.data.Folders...)
		v.Notes = append([]Note{}, workspace.data.Notes...)
		workspace.mu.Unlock()
		v.Total = len(v.Notes)
		for _, n := range v.Notes {
			v.Counts[n.FolderID]++
		}
	}
	switch {
	case v.Page == "login" || v.Page == "register":
		v.Title = "Welcome to Lexicon"
	case v.Page == "":
		v.Query = r.URL.Query().Get("q")
		v.Sort = r.URL.Query().Get("sort")
		v.FolderID, _ = strconv.Atoi(r.URL.Query().Get("folder"))
		if v.FolderID != 0 {
			v.Title = v.Folder(v.FolderID).Name
		}
		term := strings.ToLower(strings.TrimSpace(v.Query))
		if term != "" {
			v.Title = "Search results"
		}
		filtered := []Note{}
		for _, n := range v.Notes {
			if term != "" {
				if !strings.Contains(strings.ToLower(n.Title+"\n"+n.Description), term) {
					continue
				}
			} else if v.FolderID != 0 && n.FolderID != v.FolderID {
				continue
			}
			filtered = append(filtered, n)
		}
		v.Notes = filtered
		sort.SliceStable(v.Notes, func(i, j int) bool {
			if v.Sort == "az" {
				return strings.ToLower(v.Notes[i].Title) < strings.ToLower(v.Notes[j].Title)
			}
			return v.Notes[i].CreatedAt.After(v.Notes[j].CreatedAt)
		})
	case v.Page == "folders/new":
		v.Title, v.Action = "New folder", "folder"
	case v.Page == "words/new":
		if len(v.Folders) == 0 {
			websiteRedirect(w, r, "/folders/new")
			return
		}
		v.Title, v.Action = "Add word", "word"
		v.Note.FolderID, _ = strconv.Atoi(r.URL.Query().Get("folder"))
	case strings.HasPrefix(v.Page, "words/"):
		parts := strings.Split(v.Page, "/")
		id, _ := strconv.Atoi(parts[1])
		found := false
		for _, n := range v.Notes {
			if n.ID == id {
				v.Note = n
				found = true
				break
			}
		}
		if !found || len(parts) > 3 || (len(parts) == 3 && parts[2] != "edit" && parts[2] != "delete") {
			http.NotFound(w, r)
			return
		}
		v.Title = v.Note.Title
		if len(parts) == 2 {
			v.Page = "word"
		} else if parts[2] == "edit" {
			v.Page, v.Title, v.Action = "words/new", "Edit word", "word-edit"
		} else {
			v.Page, v.Title, v.Action = "delete", "Delete word?", "word-delete"
		}
	case v.Page == "news":
		v.Title = "News"
		a.news.mu.Lock()
		v.Posts = append([]announcement{}, a.news.data.Announcements...)
		a.news.mu.Unlock()
	case v.Page == "news/new" || strings.HasPrefix(v.Page, "news/"):
		if !v.Owner {
			http.Error(w, "Only the library owner can manage news.", 403)
			return
		}
		v.Title, v.Action = "New post", "news"
		if v.Page != "news/new" {
			parts := strings.Split(v.Page, "/")
			id, _ := strconv.Atoi(parts[1])
			found := false
			a.news.mu.Lock()
			for _, p := range a.news.data.Announcements {
				if p.ID == id {
					v.Post = p
					found = true
					break
				}
			}
			a.news.mu.Unlock()
			if !found || len(parts) != 3 || (parts[2] != "edit" && parts[2] != "delete") {
				http.NotFound(w, r)
				return
			}
			v.Action, v.Title = "news-edit", "Edit post"
			if parts[2] == "delete" {
				v.Page, v.Title, v.Action = "delete", "Delete post?", "news-delete"
			}
		}
		if v.Page != "delete" {
			v.Page = "news/new"
		}
	case v.Page == "profile":
		v.Title = "Your profile"
		v.Action = "profile"
	default:
		http.NotFound(w, r)
		return
	}
	// Failed submissions keep non-sensitive values in the rendered form only.
	if values != nil {
		if v.Page == "words/new" {
			v.Note.Title = values.Get("title")
			v.Note.Description = values.Get("description")
			v.Note.FolderID, _ = strconv.Atoi(values.Get("folderId"))
		}
		if v.Page == "news/new" {
			v.Post.Title = values.Get("title")
			v.Post.Body = values.Get("body")
		}
		if v.Page == "profile" {
			v.User.FullName = values.Get("full_name")
			v.User.Username = values.Get("username")
			v.User.Email = values.Get("email")
			v.User.Bio = values.Get("bio")
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "HX-Request")
	var output bytes.Buffer
	if err := pages.ExecuteTemplate(&output, "page.html", v); err != nil {
		log.Print(err)
		http.Error(w, "The page could not be rendered.", 500)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(output.Bytes())
}

// Forms call the existing API handlers in-process, retaining validation,
// authorization, origin checks, rate limits, cookies, and atomic persistence.
type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.body.Write(p)
}

func websiteRedirect(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("HX-Request") == "true" {
		location, _ := json.Marshal(map[string]string{"path": path, "target": "#app", "select": "#app", "swap": "outerHTML"})
		w.Header().Set("HX-Location", string(location))
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (a *application) websiteAction(w http.ResponseWriter, r *http.Request, api http.Handler) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, "Send a valid form up to 8 MB.", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
	} else if err := r.ParseForm(); err != nil {
		http.Error(w, "Send a valid form.", 400)
		return
	}
	fields := r.PostForm
	payload := map[string]any{}
	action := r.PathValue("action")
	method, path, back, dest := "POST", "", "", "/"
	id, err := strconv.Atoi(fields.Get("id"))
	if strings.HasSuffix(action, "-edit") || strings.HasSuffix(action, "-delete") {
		if err != nil || id < 1 {
			http.Error(w, "Invalid item.", 400)
			return
		}
	}
	copyFields := func(names ...string) {
		for _, name := range names {
			payload[name] = fields.Get(name)
		}
	}
	switch action {
	case "login":
		path, back = "/api/login", "/login"
		copyFields("identifier", "password")
	case "register":
		path, back = "/api/register", "/register"
		copyFields("full_name", "username", "email", "password")
	case "logout":
		path, back, dest = "/api/logout", "/profile", "/login"
	case "folder":
		path, back = "/api/folders", "/folders/new"
		copyFields("name")
	case "word", "word-edit", "word-delete":
		path, back = "/api/notes", "/words/new"
		if action != "word" {
			path += fmt.Sprintf("/%d", id)
			back = fmt.Sprintf("/words/%d/edit", id)
			method = "PATCH"
			dest = fmt.Sprintf("/words/%d", id)
		}
		if action == "word-delete" {
			method, back, dest = "DELETE", fmt.Sprintf("/words/%d/delete", id), "/"
		} else {
			copyFields("title", "description")
			folder, _ := strconv.Atoi(fields.Get("folderId"))
			payload["folderId"] = folder
		}
	case "news", "news-edit", "news-delete":
		path, back, dest = "/api/news", "/news/new", "/news"
		if action != "news" {
			path += fmt.Sprintf("/%d", id)
			method = "PATCH"
			back = fmt.Sprintf("/news/%d/edit", id)
		}
		if action == "news-delete" {
			method = "DELETE"
			back = fmt.Sprintf("/news/%d/delete", id)
		} else {
			copyFields("title", "body")
		}
	case "profile":
		path, back, dest, method = "/api/profile", "/profile", "/profile", "PATCH"
		copyFields("full_name", "username", "email", "bio", "current_password")
		a.mu.Lock()
		u, _ := a.userLocked(r)
		a.mu.Unlock()
		avatar := u.Profile.Avatar
		if fields.Get("remove_avatar") == "on" {
			avatar = ""
		}
		file, _, uploadErr := r.FormFile("avatar_file")
		if uploadErr == nil {
			defer file.Close()
			raw, readErr := io.ReadAll(io.LimitReader(file, 5<<20+1))
			if readErr != nil || len(raw) > 5<<20 {
				a.actionError(w, r, back, "Choose a JPEG or PNG up to 5 MB.", fields, 422)
				return
			}
			contentType := http.DetectContentType(raw)
			if contentType != "image/jpeg" && contentType != "image/png" {
				a.actionError(w, r, back, "Choose a JPEG or PNG image.", fields, 422)
				return
			}
			resized, e := resizeAvatar(raw)
			if e != nil {
				a.actionError(w, r, back, e.Error(), fields, 422)
				return
			}
			avatar = resized
		} else if uploadErr != http.ErrMissingFile && uploadErr != http.ErrNotMultipart {
			http.Error(w, "The avatar could not be read.", 400)
			return
		}
		payload["avatar"] = avatar
	default:
		http.NotFound(w, r)
		return
	}
	raw, _ := json.Marshal(payload)
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	request.URL.Path = path
	request.URL.RawQuery = ""
	request.Method = method
	request.Body = io.NopCloser(bytes.NewReader(raw))
	request.ContentLength = int64(len(raw))
	request.Header = request.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	response := &bufferedResponse{header: make(http.Header)}
	api.ServeHTTP(response, request)
	for _, cookie := range response.header.Values("Set-Cookie") {
		w.Header().Add("Set-Cookie", cookie)
	}
	if response.status >= 400 {
		if response.status == 401 && action != "login" {
			websiteRedirect(w, r, "/login")
			return
		}
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(response.body.Bytes(), &failure)
		if failure.Error == "" {
			failure.Error = "The change could not be saved. Please try again."
		}
		a.actionError(w, r, back, failure.Error, fields, 422)
		return
	}
	websiteRedirect(w, r, dest)
}
func (a *application) actionError(w http.ResponseWriter, r *http.Request, path, message string, values url.Values, status int) {
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	request.URL.Path = path
	request.Method = "GET"
	a.websitePage(w, request, message, values, status)
}

var headingPattern = regexp.MustCompile(`^ {0,3}(#{1,6})[\t ]+(.+?)\s*$`)

// Only fixed tags and HTML-escaped text enter this trusted template value.
func renderDocument(text string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	var out strings.Builder
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence := trimmed[:3]
			count := 3
			for count < len(trimmed) && trimmed[count] == trimmed[0] {
				count++
			}
			fence = trimmed[:count]
			i++
			code := []string{}
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				code = append(code, lines[i])
				i++
			}
			if i < len(lines) {
				i++
			}
			out.WriteString("<pre><code>" + template.HTMLEscapeString(strings.Join(code, "\n")) + "</code></pre>")
			continue
		}
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			fmt.Fprintf(&out, "<h%d>%s</h%d>", level, template.HTMLEscapeString(m[2]), level)
			i++
			continue
		}
		paragraph := []string{line}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !headingPattern.MatchString(lines[i]) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") && !strings.HasPrefix(strings.TrimSpace(lines[i]), "~~~") {
			paragraph = append(paragraph, lines[i])
			i++
		}
		out.WriteString("<p>" + template.HTMLEscapeString(strings.Join(paragraph, "\n")) + "</p>")
	}
	return template.HTML(out.String())
}

// Crop and resize on the server so ordinary phone photos meet the API limits.
func resizeAvatar(raw []byte) (string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 24000000 {
		return "", fmt.Errorf("Choose an image with at most 24 million pixels.")
	}
	source, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("Could not read that avatar.")
	}
	bounds := source.Bounds()
	size := min(bounds.Dx(), bounds.Dy())
	left := bounds.Min.X + (bounds.Dx()-size)/2
	top := bounds.Min.Y + (bounds.Dy()-size)/2
	cropped := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			cropped.Set(x, y, source.At(left+x*size/256, top+y*size/256))
		}
	}
	var output bytes.Buffer
	if err = jpeg.Encode(&output, cropped, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(output.Bytes()), nil
}
