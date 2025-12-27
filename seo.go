package seo

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	SitemapPath string   `json:"sitemapPath,omitempty"`
	RobotsPath  string   `json:"robotsPath,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
}

func CreateConfig() *Config {
	return &Config{
		SitemapPath: "/sitemap.xml",
		RobotsPath:  "/robots.txt",
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

type sitemapGenerator struct {
	next        http.Handler
	name        string
	sitemapPath string
	robotsPath  string
	ignores     []*regexp.Regexp
	paths       map[string]struct{}
	mu          sync.Mutex
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if config.SitemapPath == "" {
		config.SitemapPath = "/sitemap.xml"
	}
	if config.RobotsPath == "" {
		config.RobotsPath = "/robots.txt"
	}

	var ignores []*regexp.Regexp
	for _, pattern := range config.Ignore {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid ignore regex %s: %v", pattern, err)
		}
		ignores = append(ignores, re)
	}

	defaultPatterns := []string{
		`(?i)\.env`,
		`(?i)\.bak`,
		`(?i)\.old`,
		`(?i)\.example`,
		`(?i)\.exmaple`,
		`(?i)\.sample`,
		`(?i)\.tmpl`,
		`(?i)\.tpl`,
		`(?i)\.dist`,
		`(?i)\.~`,
		`(?i)\.php`,
		`(?i)\.aspx`,
		`(?i)config`,
		`(?i)wp-`,
		`(?i)sitemap`,
		`(?i)undefined`,
		`^/_next/*`,
		`\.(jpg|jpeg|png|gif|webp|svg|bmp|tif|tiff|ico|txt|php|exe|css|js|json|pdf|doc|docx|xls|xlsx|ppt|pptx|mp3|mp4|avi|mov|zip|rar|tar|gz|env|html|xml)$`,
	}
	for _, pattern := range defaultPatterns {
		re := regexp.MustCompile(pattern)
		ignores = append(ignores, re)
	}

	sg := &sitemapGenerator{
		next:        next,
		name:        name,
		sitemapPath: config.SitemapPath,
		robotsPath:  config.RobotsPath,
		ignores:     ignores,
		paths:       make(map[string]struct{}),
	}

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

	if path == sg.robotsPath {
		robotsContent := sg.buildRobotsTxt(req)
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(robotsContent))
		return
	}

	ignored := false
	for _, re := range sg.ignores {
		if re.MatchString(path) {
			ignored = true
			break
		}
	}

	scheme := req.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = req.URL.Scheme
	}
	host := req.Host
	fullURL := scheme + "://" + host + strings.TrimSuffix(path, "/")

	sw := &statusWriter{ResponseWriter: rw}
	sg.next.ServeHTTP(sw, req)

	if sw.status == 0 {
		sw.status = http.StatusOK
	}

	if !ignored && sw.status == http.StatusOK {
		sg.mu.Lock()
		sg.paths[fullURL] = struct{}{}
		sg.mu.Unlock()
	}
}

type pathInfo struct {
	loc string
}

func (sg *sitemapGenerator) buildSitemapXML(req *http.Request) []byte {
	sg.mu.Lock()
	infos := make([]pathInfo, 0, len(sg.paths))
	for p := range sg.paths {
		infos = append(infos, pathInfo{loc: p})
	}
	sg.mu.Unlock()

	var filteredInfos []pathInfo
	var base string
	if req != nil {
		scheme := req.Header.Get("X-Forwarded-Proto")
		if scheme == "" {
			scheme = req.URL.Scheme
		}
		base = scheme + "://" + req.Host
		hasRoot := false
		for _, info := range infos {
			if strings.HasPrefix(info.loc, base+"/") || info.loc == base {
				if info.loc == base {
					hasRoot = true
				}
				filteredInfos = append(filteredInfos, info)
			}
		}
		if !hasRoot {
			filteredInfos = append(filteredInfos, pathInfo{loc: base})
		}
	} else {
		filteredInfos = infos
	}

	sort.Slice(filteredInfos, func(i, j int) bool {
		return filteredInfos[i].loc < filteredInfos[j].loc
	})

	type URL struct {
		Loc      string  `xml:"loc"`
		Lastmod  string  `xml:"lastmod"`
		Priority float64 `xml:"priority"`
	}
	type URLSet struct {
		XMLName xml.Name `xml:"urlset"`
		Xmlns   string   `xml:"xmlns,attr"`
		URLs    []URL    `xml:"url"`
	}

	urlset := URLSet{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
	}

	now := time.Now().UTC()
	lastmodStr := now.Format("2006-01-02T15:04:05Z")

	for _, info := range filteredInfos {
		priority := 0.8
		if base != "" && info.loc == base {
			priority = 1.0
		}
		urlset.URLs = append(urlset.URLs, URL{Loc: info.loc, Lastmod: lastmodStr, Priority: priority})
	}

	output, err := xml.MarshalIndent(urlset, "", "  ")
	if err != nil {
		return nil
	}

	return []byte(xml.Header + string(output))
}

func (sg *sitemapGenerator) buildRobotsTxt(req *http.Request) string {
	scheme := req.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = req.URL.Scheme
	}
	host := req.Host
	sitemapURL := scheme + "://" + host + sg.sitemapPath

	return fmt.Sprintf("User-agent: *\nSitemap: %s\n", sitemapURL)
}
