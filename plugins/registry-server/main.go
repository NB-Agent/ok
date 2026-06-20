// OK v4 Plugin Registry Server
// Serves plugin metadata for `plugins.ok.sh` and private registries.
//
// Usage:
//
//	ok-registry serve --port 8080 --dir ./plugins
//	# Then configure ok.toml:
//	# [plugins.registries]
//	# official = "http://localhost:8080/v1/index.json"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var port = flag.Int("port", 8080, "HTTP port")
var dir = flag.String("dir", ".", "plugin directory containing plugin.json files")

func main() {
	flag.Parse()

	http.HandleFunc("/v1/index.json", handleIndex)
	http.HandleFunc("/v1/plugins", handleList)
	http.HandleFunc("/v1/plugins/", handlePlugin)
	http.HandleFunc("/health", handleHealth)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("OK Registry serving on %s (dir=%s)", addr, *dir)
	log.Fatal(http.ListenAndServe(addr, nil))
}

type PluginEntry struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	URL          string   `json:"url"`
	Checksum     string   `json:"checksum"`
	Homepage     string   `json:"homepage"`
	Tags         []string `json:"tags"`
	MinOKVersion string   `json:"min_ok_version"`
	License      string   `json:"license"`
	Author       string   `json:"author"`
	Tools        []string `json:"tools"`
	Updated      string   `json:"updated"`
}

type RegistryIndex struct {
	Version string        `json:"version"`
	Updated string        `json:"updated"`
	Plugins []PluginEntry `json:"plugins"`
}

func loadPlugins() ([]PluginEntry, error) {
	entries, err := os.ReadDir(*dir)
	if err != nil {
		return nil, err
	}
	var plugins []PluginEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pj := filepath.Join(*dir, e.Name(), "plugin.json")
		data, err := os.ReadFile(pj)
		if err != nil {
			continue
		}
		var p PluginEntry
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		if p.Version == "" {
			p.Version = "1.0.0"
		}
		if p.URL == "" {
			p.URL = fmt.Sprintf("https://plugins.ok.sh/download/%s/%s.tar.gz", p.Name, p.Version)
		}
		if p.Updated == "" {
			p.Updated = time.Now().UTC().Format(time.RFC3339)
		}
		plugins = append(plugins, p)
	}
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})
	return plugins, nil
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	plugins, err := loadPlugins()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	idx := RegistryIndex{
		Version: "1.0.0",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Plugins: plugins,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(idx)
}

func handleList(w http.ResponseWriter, r *http.Request) {
	plugins, err := loadPlugins()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugins)
}

func handlePlugin(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Path)
	plugins, err := loadPlugins()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, p := range plugins {
		if p.Name == name || p.Name == "@ok/"+name {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(p)
			return
		}
	}
	http.Error(w, fmt.Sprintf("plugin %q not found", name), 404)
}
