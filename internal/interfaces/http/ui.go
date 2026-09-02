package api

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
)

//go:embed ui/*
var uiFiles embed.FS

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	filename, err := uiPageName(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	index, err := uiFiles.ReadFile(path.Join("ui", filename))
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func uiPageName(requestPath string) (string, error) {
	switch requestPath {
	case "/":
		return "index.html", nil
	case "/login":
		return "login.html", nil
	case "/users":
		return "users.html", nil
	case "/workers":
		return "workers.html", nil
	case "/jobs":
		return "jobs.html", nil
	case "/tasks":
		return "tasks.html", nil
	case "/configs":
		return "configs.html", nil
	case "/knowledge":
		return "knowledge.html", nil
	default:
		return "", errors.New("page not found")
	}
}

func (s *Server) staticAssetsHandler() http.Handler {
	staticFS, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return http.NotFoundHandler()
	}

	return http.StripPrefix("/static/", http.FileServerFS(staticFS))
}
