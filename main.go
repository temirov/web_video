package main

import (
	"context"
	"encoding/json"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Video represents a single video entry from the JSON file.
type Video struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	FileName    string `json:"fileName"`
}

// PageData is the payload passed into the HTML template.
type PageData struct {
	Title  string
	Videos []Video
}

const (
	defaultServerAddress      = ":8080"
	defaultVideosJSONFileName = "videos.json"
	defaultStaticDirectory    = "static"
	defaultTemplatesDirectory = "templates"
	defaultTitle              = "I am an American and I don’t give a fuck!"
)

// loadAndValidateVideos reads the JSON file at the provided path, validates each entry,
// and returns the sanitized slice of Video objects. Invalid or missing video files are skipped with a warning.
func loadAndValidateVideos(videosJSONPath string, staticVideosDirectory string) ([]Video, error) {
	contentBytes, readError := os.ReadFile(videosJSONPath)
	if readError != nil {
		return nil, readError
	}

	var rawVideos []Video
	if unmarshalError := json.Unmarshal(contentBytes, &rawVideos); unmarshalError != nil {
		return nil, unmarshalError
	}

	validatedVideos := make([]Video, 0, len(rawVideos))
	for _, candidateVideo := range rawVideos {
		if strings.TrimSpace(candidateVideo.FileName) == "" {
			log.Printf("warning: skipping video with empty fileName: title=%q", candidateVideo.Title)
			continue
		}

		baseFileName := filepath.Base(candidateVideo.FileName)
		if baseFileName != candidateVideo.FileName {
			log.Printf("warning: skipping video with disallowed path in fileName: %q", candidateVideo.FileName)
			continue
		}

		candidatePath := filepath.Join(staticVideosDirectory, "videos", baseFileName)
		if _, statError := os.Stat(candidatePath); os.IsNotExist(statError) {
			log.Printf("warning: video file does not exist, skipping: %s", candidatePath)
			continue
		} else if statError != nil {
			log.Printf("warning: unable to stat video file %s: %v (skipping)", candidatePath, statError)
			continue
		}

		if strings.TrimSpace(candidateVideo.Title) == "" {
			log.Printf("warning: skipping video with empty title for fileName=%q", candidateVideo.FileName)
			continue
		}
		if strings.TrimSpace(candidateVideo.Description) == "" {
			log.Printf("warning: skipping video with empty description for fileName=%q", candidateVideo.FileName)
			continue
		}

		validatedVideos = append(validatedVideos, candidateVideo)
	}

	return validatedVideos, nil
}

// watchVideosJSON sets up a watcher on videosJSONPath. When the file changes, it reloads
// and validates the list and swaps it into videoStore atomically. It debounces rapid consecutive events.
func watchVideosJSON(videosJSONPath string, staticVideosDirectory string, videoStore *atomic.Value, watcherStartedSignal chan struct{}) {
	fileWatcher, watcherError := fsnotify.NewWatcher()
	if watcherError != nil {
		log.Printf("error: failed to create fsnotify watcher: %v", watcherError)
		close(watcherStartedSignal)
		return
	}
	defer fileWatcher.Close()

	watchDirectory := filepath.Dir(videosJSONPath)
	if addErr := fileWatcher.Add(watchDirectory); addErr != nil {
		log.Printf("error: cannot watch directory %s: %v", watchDirectory, addErr)
		close(watcherStartedSignal)
		return
	}

	// Signal that watcher is ready.
	close(watcherStartedSignal)

	var debounceTimerMutex sync.Mutex
	var debounceTimer *time.Timer

	triggerReload := func() {
		reloadedVideos, loadError := loadAndValidateVideos(videosJSONPath, staticVideosDirectory)
		if loadError != nil {
			log.Printf("error: dynamic reload failed to load videos.json: %v", loadError)
			return
		}
		videoStore.Store(reloadedVideos)
		log.Printf("dynamic reload: updated videos list with %d validated video(s)", len(reloadedVideos))
	}

	for {
		select {
		case event, ok := <-fileWatcher.Events:
			if !ok {
				return
			}
			// Interested if the specific file was written or replaced/renamed.
			if !strings.Contains(event.Name, filepath.Base(videosJSONPath)) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}

			debounceTimerMutex.Lock()
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(400*time.Millisecond, func() {
				triggerReload()
			})
			debounceTimerMutex.Unlock()

		case watchError, ok := <-fileWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("warning: file watcher error: %v", watchError)
		}
	}
}

func main() {
	serverAddressFlag := flag.String("address", defaultServerAddress, "address to listen on, e.g. :8080")
	videosJSONPathFlag := flag.String("videos", defaultVideosJSONFileName, "path to videos.json")
	staticDirectoryFlag := flag.String("static", defaultStaticDirectory, "static assets directory")
	templatesDirectoryFlag := flag.String("templates", defaultTemplatesDirectory, "templates directory")
	pageTitleFlag := flag.String("title", defaultTitle, "page title to display")
	flag.Parse()

	staticDirectory := *staticDirectoryFlag
	templatesDirectory := *templatesDirectoryFlag
	videosJSONPath := *videosJSONPathFlag

	// Load and validate videos at startup.
	initialVideoSlice, initialLoadError := loadAndValidateVideos(videosJSONPath, staticDirectory)
	if initialLoadError != nil {
		log.Fatalf("Failed to load video metadata from %s: %v", videosJSONPath, initialLoadError)
	}
	log.Printf("Initial load: %d validated video(s).", len(initialVideoSlice))

	// Prepare atomic store and put initial value.
	var videoStore atomic.Value
	videoStore.Store(initialVideoSlice)

	// Start watcher to dynamically reload on changes.
	watcherReadyChannel := make(chan struct{})
	go watchVideosJSON(videosJSONPath, staticDirectory, &videoStore, watcherReadyChannel)
	<-watcherReadyChannel // wait until watcher is initialized (or failed to initialize)

	// Parse template once.
	templatePath := filepath.Join(templatesDirectory, "index.html")
	parsedTemplate, parseError := template.ParseFiles(templatePath)
	if parseError != nil {
		log.Fatalf("Failed to parse template %s: %v", templatePath, parseError)
	}

	// File server for static content.
	fileServerHandler := http.FileServer(http.Dir(staticDirectory))
	http.Handle("/static/", http.StripPrefix("/static/", fileServerHandler))

	// Health endpoint.
	http.HandleFunc("/healthz", func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusOK)
		_, _ = responseWriter.Write([]byte("ok"))
	})

	// Main handler.
	http.HandleFunc("/", func(responseWriter http.ResponseWriter, request *http.Request) {
		currentVideosInterface := videoStore.Load()
		currentVideos, castOK := currentVideosInterface.([]Video)
		if !castOK {
			http.Error(responseWriter, "Internal Server Error", http.StatusInternalServerError)
			log.Printf("type assertion failed on videoStore content")
			return
		}

		pageData := PageData{
			Title:  *pageTitleFlag,
			Videos: currentVideos,
		}

		executionError := parsedTemplate.Execute(responseWriter, pageData)
		if executionError != nil {
			http.Error(responseWriter, "Internal Server Error", http.StatusInternalServerError)
			log.Printf("template execution error: %v", executionError)
		}
	})

	// Build the HTTP server with timeouts.
	httpServer := &http.Server{
		Addr:         *serverAddressFlag,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Channel for OS signals.
	shutdownSignalChannel := make(chan os.Signal, 1)
	signal.Notify(shutdownSignalChannel, os.Interrupt, os.Kill)

	// Run server.
	serverShutdownDoneChannel := make(chan struct{})
	go func() {
		log.Printf("Starting server on %s", *serverAddressFlag)
		if listenError := httpServer.ListenAndServe(); listenError != nil && listenError != http.ErrServerClosed {
			log.Fatalf("HTTP server ListenAndServe failure: %v", listenError)
		}
		close(serverShutdownDoneChannel)
	}()

	// Wait for shutdown signal.
	receivedSignal := <-shutdownSignalChannel
	log.Printf("Received signal %v, initiating graceful shutdown", receivedSignal)

	shutdownContext, cancelFunction := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunction()

	if shutdownError := httpServer.Shutdown(shutdownContext); shutdownError != nil {
		log.Printf("Graceful shutdown failed: %v", shutdownError)
		if forceCloseError := httpServer.Close(); forceCloseError != nil {
			log.Printf("Force close also failed: %v", forceCloseError)
		}
	}

	<-serverShutdownDoneChannel
	log.Println("Server shutdown complete.")
}
