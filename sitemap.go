package traefik_sitemap_generator

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Config struct {
	OutputFile  string   `json:"outputFile,omitempty"`
	SitemapPath string   `json:"sitemapPath,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
}

func CreateConfig() *Config {
	return &Config{
		SitemapPath: "/sitemap.xml",
	}
}

type sitemapGenerator struct {
	next        http.Handler
	name        string
	outputFile  string
	sitemapPath string
	ignores     []*regexp.Regexp
	paths       map[string]struct{}
	mu          sync.Mutex
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if config.OutputFile == "" {
		return nil, fmt.Errorf("outputFile is required")
	}
	if config.SitemapPath == "" {
		config.SitemapPath = "/sitemap.xml"
	}

	var ignores []*regexp.Regexp
	for _, pattern := range config.Ignore {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid ignore regex %s: %v", pattern, err)
		}
		ignores = append(ignores, re)
	}

	sg := &sitemapGenerator{
		next:        next,
		name:        name,
		outputFile:  config.OutputFile,
		sitemapPath: config.SitemapPath,
		ignores:     ignores,
		paths:       make(map[string]struct{}),
	}

	go sg.generateSitemapPeriodically()

	return sg, nil
}

func (sg *sitemapGenerator) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	path := req.URL.Path

	if path == sg.sitemapPath {
		xmlContent := sg.buildSitemapXML(req)
		if xmlContent == nil {
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		rw.Header().Set("Content-Type", "application/xml")
		rw.WriteHeader(http.StatusOK)
		rw.Write(xmlContent)
		return
	}

	ignored := false
	for _, re := range sg.ignores {
		if re.MatchString(path) {
			ignored = true
			break
		}
	}

	if !ignored {
		scheme := req.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = req.URL.Scheme
		}
		host := req.Host
		fullURL := scheme + "://" + host + strings.TrimSuffix(path, "/")
		sg.mu.Lock()
		sg.paths[fullURL] = struct{}{}
		sg.mu.Unlock()
	}

	sg.next.ServeHTTP(rw, req)
}

func (sg *sitemapGenerator) generateSitemapPeriodically() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		<-ticker.C
		sg.generateSitemap()
	}
}

func (sg *sitemapGenerator) generateSitemap() {
	xmlContent := sg.buildSitemapXML(nil)
	if xmlContent == nil {
		return
	}

	dir := filepath.Dir(sg.outputFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	if err := os.WriteFile(sg.outputFile, xmlContent, 0644); err != nil {
		return
	}
}

func (sg *sitemapGenerator) buildSitemapXML(req *http.Request) []byte {
	sg.mu.Lock()
	paths := make([]string, 0, len(sg.paths))
	for p := range sg.paths {
		paths = append(paths, p)
	}
	sg.mu.Unlock()

	var filteredPaths []string
	if req != nil {
		scheme := req.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = req.URL.Scheme
		}
		base := scheme + "://" + req.Host + "/"
		for _, p := range paths {
			if strings.HasPrefix(p, base) {
				filteredPaths = append(filteredPaths, p)
			}
		}
	} else {
		filteredPaths = paths
	}

	type URL struct {
		Loc string `xml:"loc"`
	}
	type URLSet struct {
		XMLName xml.Name `xml:"urlset"`
		Xmlns   string   `xml:"xmlns,attr"`
		URLs    []URL    `xml:"url"`
	}

	urlset := URLSet{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
	}

	for _, p := range filteredPaths {
		urlset.URLs = append(urlset.URLs, URL{Loc: p})
	}

	output, err := xml.MarshalIndent(urlset, "", "  ")
	if err != nil {
		return nil
	}

	return []byte(xml.Header + string(output))
}
