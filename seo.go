package seo

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	gtmScriptTemplate = `<!-- Google Tag Manager -->
<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':
new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],
j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src=
'https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);
})(window,document,'script','dataLayer','%s');</script>
<!-- End Google Tag Manager -->`

	gtmNoscriptTemplate = `<!-- Google Tag Manager (noscript) -->
<noscript><iframe src="https://www.googletagmanager.com/ns.html?id=%s"
height="0" width="0" style="display:none;visibility:hidden"></iframe></noscript>
<!-- End Google Tag Manager (noscript) -->`
)

type Config struct {
	SitemapPath string   `json:"sitemapPath,omitempty"`
	RobotsPath  string   `json:"robotsPath,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
	GTMID       string   `json:"gtmID,omitempty"`
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

type modifyingWriter struct {
	http.ResponseWriter
	status int
	body   *bytes.Buffer
}

func (w *modifyingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(b)
}

func (w *modifyingWriter) WriteHeader(code int) {
	w.status = code
}

type sitemapGenerator struct {
	next        http.Handler
	name        string
	sitemapPath string
	robotsPath  string
	ignores     []*regexp.Regexp
	paths       map[string]struct{}
	gtmID       string
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
		gtmID:       config.GTMID,
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

	mw := &modifyingWriter{
		ResponseWriter: rw,
		body:           bytes.NewBuffer([]byte{}),
	}
	sg.next.ServeHTTP(mw, req)

	if mw.status == 0 {
		mw.status = http.StatusOK
	}

	contentType := rw.Header().Get("Content-Type")
	contentEncoding := rw.Header().Get("Content-Encoding")

	var bodyBytes []byte = mw.body.Bytes()
	var isGzipped bool = strings.EqualFold(contentEncoding, "gzip")

	if isGzipped {
		reader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
		if err != nil {
			rw.WriteHeader(mw.status)
			rw.Write(bodyBytes)
			return
		}
		defer reader.Close()
		decompressed, err := io.ReadAll(reader)
		if err != nil {
			rw.WriteHeader(mw.status)
			rw.Write(bodyBytes)
			return
		}
		bodyBytes = decompressed
	}

	bodyStr := string(bodyBytes)

	if sg.gtmID != "" && strings.HasPrefix(strings.ToLower(contentType), "text/html") && mw.status == http.StatusOK {
		gtmScript := fmt.Sprintf(gtmScriptTemplate, sg.gtmID)
		gtmNoscript := fmt.Sprintf(gtmNoscriptTemplate, sg.gtmID)

		modified := strings.Replace(bodyStr, "</head>", gtmScript+"</head>", 1)

		re := regexp.MustCompile(`(?i)<body\b[^>]*>`)
		match := re.FindStringIndex(modified)
		if match != nil {
			insertPos := match[1]
			modified = modified[:insertPos] + gtmNoscript + modified[insertPos:]
		}

		bodyBytes = []byte(modified)

		if isGzipped {
			var gzippedBuf bytes.Buffer
			writer := gzip.NewWriter(&gzippedBuf)
			_, err := writer.Write(bodyBytes)
			if err != nil {
				rw.WriteHeader(mw.status)
				rw.Write(mw.body.Bytes())
				return
			}
			writer.Close()
			bodyBytes = gzippedBuf.Bytes()
		}

		rw.Header().Del("Content-Length")
		rw.WriteHeader(mw.status)
		rw.Write(bodyBytes)
	} else {
		rw.WriteHeader(mw.status)
		rw.Write(mw.body.Bytes())
	}

	if !ignored && mw.status == http.StatusOK {
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
