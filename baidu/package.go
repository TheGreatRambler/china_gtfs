package baidu_client

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/marcozac/go-jsonc"
)

// Created using https://mholt.github.io/json-to-go/
type BaiduSubwayCities struct {
	Result struct {
		Type          string `json:"type"`
		Error         string `json:"error"`
		SubwayVersion string `json:"subwayVersion"`
	} `json:"result"`
	SubwaysCity struct {
		Cities []struct {
			CnName string `json:"cn_name"`
			Cename string `json:"cename"`
			Code   int    `json:"code"`
			Cpre   string `json:"cpre"`
			CxfDis int    `json:"cxfDis,omitempty"`
		} `json:"cities"`
	} `json:"subways_city"`
}

type BaiduAutocomplete struct {
	Content []BaiduAutocompleteEntry `json:"content"`
}

type BaiduAutocompleteEntry struct {
	Addr     string `json:"addr"`
	AreaName string `json:"area_name"`
	GeoType  int    `json:"geo_type"`
	Name     string `json:"name"`
	UID      string `json:"uid"`
}

type BaiduAutocompleteType struct {
	S []string `json:"s"`
}

type CityUIDMapping struct {
	BaiduID        string
	MetromanCode   string
	ChelaileCode   string
	EnglishName    string
	SimplifiedName string
}

type BaiduServer struct {
	TextTemplates                 *template.Template
	Auth                          string
	Headers                       map[string]string
	BaiduSubwayCities             BaiduSubwayCities
	CityUIDMappings               []CityUIDMapping
	CityUIDMappingsByMetromanCode map[string]CityUIDMapping
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

	s.BaiduSubwayCities, err = s.GetBaiduSubwayCities()
	if err != nil {
		return &BaiduServer{}, fmt.Errorf("could not get subway cities: %v", err)
	}

	s.CityUIDMappings, err = s.LoadCityUIDMappings()
	if err != nil {
		return &BaiduServer{}, fmt.Errorf("could not load city UID mappings: %v", err)
	}

	s.CityUIDMappingsByMetromanCode = make(map[string]CityUIDMapping)
	for _, mapping := range s.CityUIDMappings {
		s.CityUIDMappingsByMetromanCode[mapping.MetromanCode] = mapping
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

func (s *BaiduServer) GetAutocomplete(metroman_city string, search_query string) (BaiduAutocomplete, error) {
	var url_buf bytes.Buffer
	err := s.TextTemplates.ExecuteTemplate(&url_buf, "baidu_autocomplete_url.gotxt",
		map[string]interface{}{
			"SearchQuery": search_query,
			"Auth":        s.Auth,
			"CityID":      s.CityUIDMappingsByMetromanCode[metroman_city].BaiduID,
			"Timestamp":   time.Now().UnixMilli(),
		})
	if err != nil {
		return BaiduAutocomplete{}, fmt.Errorf("could not parse autocomplete request template: %v", err)
	}

	url := strings.NewReplacer("\t", "", "\n", "", "\r\n", "").Replace(url_buf.String())

	// Create a new request to Baidu Maps
	// Remove just tabs and newlines
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return BaiduAutocomplete{}, fmt.Errorf("could not create autocomplete request: %v", err)
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
		return BaiduAutocomplete{}, fmt.Errorf("could not perform autocomplete request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BaiduAutocomplete{}, fmt.Errorf("could not read autocomplete request: %v", err)
	}

	//os.WriteFile("autocomplete_test.test.json", body, 0644)

	autocomplete := BaiduAutocomplete{}
	err = json.Unmarshal(body, &autocomplete)
	if err != nil {
		return BaiduAutocomplete{}, fmt.Errorf("could not parse autocomplete: %v", err)
	}

	return autocomplete, nil
}

// Given an instance of autocomplete return the first station (using `geo_type` == 2)
func GetAutocompleteStation(autocomplete BaiduAutocomplete) (BaiduAutocompleteEntry, bool) {
	for _, entry := range autocomplete.Content {
		if entry.GeoType == 2 {
			return entry, true
		}
	}

	return BaiduAutocompleteEntry{}, false
}

func (s *BaiduServer) GetAutocompleteType(search_query string) ([]string, error) {
	var url_buf bytes.Buffer
	err := s.TextTemplates.ExecuteTemplate(&url_buf, "baidu_autocomplete_type_url.gotxt",
		map[string]interface{}{
			"SearchQuery": search_query,
			"Auth":        s.Auth,
			"Timestamp":   time.Now().UnixMilli(),
		})
	if err != nil {
		return []string{}, fmt.Errorf("could not parse autocomplete type request template: %v", err)
	}

	url := strings.NewReplacer("\t", "", "\n", "", "\r\n", "").Replace(url_buf.String())

	// Create a new request to Baidu Maps
	// Remove just tabs and newlines
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return []string{}, fmt.Errorf("could not create autocomplete type request: %v", err)
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
		return []string{}, fmt.Errorf("could not perform autocomplete type request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []string{}, fmt.Errorf("could not read autocomplete type request: %v", err)
	}

	autocomplete_type := BaiduAutocompleteType{}
	err = json.Unmarshal(body, &autocomplete_type)
	if err != nil {
		return []string{}, fmt.Errorf("could not parse autocomplete type: %v", err)
	}

	return autocomplete_type.S, nil
}

// Given some typing autocomplete results return the first likely station (using the existence of "站")
func GetAutocompleteTypeStation(autocomplete_entries []string) (string, bool) {
	for _, entry := range autocomplete_entries {
		if strings.Contains(entry, "站") {
			split := strings.Split(entry, "$")
			if len(split) > 4 {
				return split[4], true
			}
		}
	}

	return "", false
}

func (s *BaiduServer) GetBaiduSubwayCities() (BaiduSubwayCities, error) {
	// Create a new request to Baidu Maps
	// Remove just tabs and newlines
	req, err := http.NewRequest("GET",
		fmt.Sprintf("https://map.baidu.com/?qt=subwayscity&t=%d&auth=%s&pcevaname=pc4.1&newfrom=zhuzhan_webmap", time.Now().UnixMilli(), s.Auth),
		nil)
	if err != nil {
		return BaiduSubwayCities{}, fmt.Errorf("could not create subway cities request: %v", err)
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
		return BaiduSubwayCities{}, fmt.Errorf("could not perform subway cities request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BaiduSubwayCities{}, fmt.Errorf("could not read subway cities request: %v", err)
	}

	subway_cities := BaiduSubwayCities{}
	err = json.Unmarshal(body, &subway_cities)
	if err != nil {
		return BaiduSubwayCities{}, fmt.Errorf("could not parse subway cities: %v", err)
	}

	return subway_cities, nil
}

func (s *BaiduServer) LoadCityUIDMappings() ([]CityUIDMapping, error) {
	file, err := os.Open("baidu_city_uid_to_city.csv")
	if err != nil {
		return nil, fmt.Errorf("could not open CSV file: %v", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("could not read CSV file: %v", err)
	}

	if len(records) < 2 {
		return []CityUIDMapping{}, nil
	}

	mappings := make([]CityUIDMapping, 0, len(records)-1)
	for i := 1; i < len(records); i++ {
		record := records[i]
		if len(record) < 5 {
			continue
		}
		mapping := CityUIDMapping{
			BaiduID:        record[0],
			MetromanCode:   record[1],
			ChelaileCode:   record[2],
			EnglishName:    record[3],
			SimplifiedName: record[4],
		}
		mappings = append(mappings, mapping)
	}

	return mappings, nil
}
