package baidu_client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/marcozac/go-jsonc"
)

type BaiduServer struct {
	TextTemplates *template.Template
	Auth          string
	Headers       map[string]string
}

func CreateServer() (*BaiduServer, error) {
	text_templates, err := template.ParseGlob("*.gotxt")
	if err != nil {
		return &BaiduServer{}, fmt.Errorf("could not construct text templates: %v", err)
	}

	auth, headers, err := GetAuthAndHeaders(text_templates)
	if err != nil {
		return &BaiduServer{}, fmt.Errorf("could not get auth or headers: %v", err)
	}

	s := &BaiduServer{
		TextTemplates: text_templates,
		Auth:          auth,
		Headers:       headers,
	}
	return s, nil
}

func GetAuthAndHeaders(templates *template.Template) (string, map[string]string, error) {
	// Get our headers
	// Some of these values are hardcoded for now
	var headers_buf bytes.Buffer
	err := templates.ExecuteTemplate(&headers_buf, "baidu_headers.gotxt",
		map[string]interface{}{
			"BaiduID": ":FG=1",
			"Referer": "https://map.baidu.com",
		})
	if err != nil {
		return "", map[string]string{}, fmt.Errorf("could not execute headers template: %v", err)
	}

	// We explicitly allow comments
	headers_sanitized, err := jsonc.Sanitize(headers_buf.Bytes())
	if err != nil {
		return "", map[string]string{}, fmt.Errorf("could not sanitize headers JSON: %v", err)
	}

	// JSON parse these headers (must be map of strings)
	headers_map := make(map[string]string)
	err = json.Unmarshal(headers_sanitized, &headers_map)
	if err != nil {
		return "", map[string]string{}, fmt.Errorf("could not unmarshal headers JSON: %v", err)
	}

	// Get Baidu Maps auth token
	homepage_req, err := http.NewRequest("GET", "https://map.baidu.com", nil)
	if err != nil {
		return "", map[string]string{}, fmt.Errorf("could not request homepage: %v", err)
	}

	// Add standard Baidu Maps headers
	for name, header := range headers_map {
		homepage_req.Header.Add(name, header)
	}

	// Forward the request to Baidu Maps
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	homepage_resp, err := client.Do(homepage_req)
	if err != nil {
		return "", map[string]string{}, fmt.Errorf("could not request homepage for auth token: %v", err)
	}
	defer homepage_resp.Body.Close()

	homepage_body, err := io.ReadAll(homepage_resp.Body)
	if err != nil {
		return "", map[string]string{}, fmt.Errorf("could not read auth token: %v", err)
	}

	// Extract auth string
	WINDOW_AUTH := "window.AUTH = \""
	window_auth_index := strings.Index(string(homepage_body), WINDOW_AUTH)
	closing_quote_index := window_auth_index + strings.Index(string(homepage_body)[window_auth_index+len(WINDOW_AUTH):], "\"")
	auth := string(homepage_body)[window_auth_index+len(WINDOW_AUTH) : closing_quote_index]

	// Return the request with our headers and the auth token
	return auth, headers_map, nil
}

func (s *BaiduServer) GetAutocomplete(search_query string) ([]string, error) {
	var url_buf bytes.Buffer
	err := s.TextTemplates.ExecuteTemplate(&url_buf, "baidu_autocomplete_url.gotxt",
		map[string]interface{}{
			"SearchQuery": search_query,
			"Auth":        s.Auth,
			"Timestamp":   time.Now().UnixMilli(),
		})
	if err != nil {
		return []string{}, fmt.Errorf("could not parse transit request template: %v", err)
	}

	url := strings.NewReplacer("\t", "", "\n", "", "\r\n", "").Replace(url_buf.String())

	// Create a new request to Baidu Maps
	// Remove just tabs and newlines
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return []string{}, fmt.Errorf("could not create route request: %v", err)
	}

	// Add standard Baidu Maps headers
	for name, header := range s.Headers {
		req.Header.Add(name, header)
	}

	// Forward the request to Baidu Maps
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return []string{}, fmt.Errorf("could not perform route request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []string{}, fmt.Errorf("could not read route request: %v", err)
	}

	//os.WriteFile("autocomplete_test.test.json", body, 0644)

	return []string{string(body)}, nil
}
