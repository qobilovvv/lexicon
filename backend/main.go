package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Folder struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Note struct {
	ID          int       `json:"id"`
	FolderID    int       `json:"folderId"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
}

type data struct {
	Folders []Folder `json:"folders"`
	Notes   []Note   `json:"notes"`
	NextID  int      `json:"nextId"`
}

type store struct {
	mu   sync.Mutex
	path string
	data data
}

var colors = []string{"blue", "purple", "green", "orange"}

func exampleData() data {
	day := time.Now().UTC()
	return data{
		Folders: []Folder{{1, "English", "blue"}, {2, "French", "purple"}, {3, "Everyday words", "green"}},
		Notes: []Note{
			{9, 1, "Serendipity", "Finding something good or valuable when you weren't looking for it.", day},
			{8, 1, "Ephemeral", "Lasting for a very short time. “The beauty of the cherry blossoms is ephemeral.”", day.AddDate(0, 0, -1)},
			{7, 2, "Dépaysement", "The feeling of being somewhere unfamiliar, away from what you know.", day.AddDate(0, 0, -2)},
			{6, 1, "Petrichor", "The earthy smell that follows rain on dry ground.", day.AddDate(0, 0, -3)},
			{5, 3, "Ripple effect", "A small action that sets off a series of wider effects.", day.AddDate(0, 0, -4)},
			{4, 2, "Flâner", "To stroll without a particular destination.", day.AddDate(0, 0, -5)},
		},
		NextID: 10,
	}
}

func (s *store) save(next data) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".lexicon-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	writeErr := json.NewEncoder(file).Encode(next)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return err
	}
	s.data = next
	return nil
}

func reply(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, code int, message string) {
	reply(w, code, map[string]string{"error": message})
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Send a valid JSON request.")
		return false
	}
	return true
}

func (s *store) commit(w http.ResponseWriter, next data, status int) {
	if err := s.save(next); err != nil {
		log.Print(err)
		fail(w, 500, "Changes could not be saved.")
		return
	}
	reply(w, status, next)
}

func (s *store) folders(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		fail(w, 400, "Enter a folder name up to 60 characters.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	for _, folder := range next.Folders {
		if strings.EqualFold(folder.Name, name) {
			fail(w, 409, "A folder with that name already exists.")
			return
		}
	}
	next.Folders = append(next.Folders, Folder{next.NextID, name, colors[len(next.Folders)%len(colors)]})
	next.NextID++
	s.commit(w, next, 201)
}

func (s *store) notes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	index := -1
	if r.Method != http.MethodPost {
		id, _ := strconv.Atoi(r.PathValue("id"))
		for i, note := range next.Notes {
			if note.ID == id {
				index = i
				break
			}
		}
		if index == -1 {
			fail(w, 404, "Word not found.")
			return
		}
	}
	next.Notes = append([]Note{}, next.Notes...)
	if r.Method == http.MethodDelete {
		next.Notes = append(next.Notes[:index], next.Notes[index+1:]...)
		s.commit(w, next, 200)
		return
	}
	var input struct {
		FolderID    int    `json:"folderId"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 120 || utf8.RuneCountInString(input.Description) > 4000 {
		fail(w, 400, "Enter a title up to 120 characters and a description up to 4,000 characters.")
		return
	}
	found := false
	for _, folder := range next.Folders {
		if folder.ID == input.FolderID {
			found = true
			break
		}
	}
	if !found {
		fail(w, 400, "Choose an existing folder.")
		return
	}
	if index >= 0 {
		note := &next.Notes[index]
		note.Title, note.Description, note.FolderID = input.Title, input.Description, input.FolderID
		s.commit(w, next, 200)
		return
	}
	next.Notes = append([]Note{{next.NextID, input.FolderID, input.Title, input.Description, time.Now().UTC()}}, next.Notes...)
	next.NextID++
	s.commit(w, next, 201)
}

func main() {
	path := os.Getenv("LEXICON_DATA_FILE")
	if path == "" {
		path = "data/lexicon.json"
	}
	s, err := loadWorkspace(path, true)
	if err != nil {
		log.Fatal(err)
	}
	app, err := newApplication(s, path+".accounts.json")
	if err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LEXICON_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: address, Handler: app.website(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second}
	log.Printf("Lexicon listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
