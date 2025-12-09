package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/davecgh/go-spew/spew"
	"github.com/geops/gtfsparser"
	"github.com/geops/gtfsparser/gtfs"
	"golang.org/x/text/unicode/norm"
	"tgrcode.com/baidu_client"
)

// date/time for OTP GraphQL plan() – GraphQL wants YYYY-MM-DD and HH:MM (24h)
const otp_date = "2025-12-02"
const otp_time = "08:30"

type OtpLeg struct {
	Mode string `json:"mode"`
	From struct {
		Name string `json:"name"`
	} `json:"from"`
	To struct {
		Name string `json:"name"`
	} `json:"to"`
}

type OtpItinerary struct {
	Duration float64  `json:"duration"`
	Legs     []OtpLeg `json:"legs"`
}

type OtpPlan struct {
	Itineraries []OtpItinerary `json:"itineraries"`
}

type OtpResponse struct {
	Plan  *OtpPlan `json:"plan"`
	Error *struct {
		Msg string `json:"msg"`
	} `json:"error"`
}

// wrapper for GraphQL response
type OtpGraphqlResponse struct {
	Data struct {
		Plan *struct {
			Itineraries []OtpItinerary `json:"itineraries"`
		} `json:"plan"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type RouteLeg struct {
	LineName   string
	FromName   string
	ToName     string
	BoardTime  string
	AlightTime string
}

func main() {
	rand.Seed(42)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Kill OTP if this process receives CTRL+C or SIGTERM
	sig_ch := make(chan os.Signal, 1)
	signal.Notify(sig_ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sig_ch
		log.Printf("received signal %v, shutting down...", s)
		cancel()
	}()

	otp_cmd, err := startOtpContainer(ctx)
	if err != nil {
		log.Fatalf("failed to start OTP container: %v", err)
	}
	defer func() {
		if otp_cmd.Process != nil {
			_ = otp_cmd.Process.Kill()
		}
	}()

	if err := waitForOtp(); err != nil {
		log.Fatalf("OTP did not come online: %v", err)
	}

	// Initialize Baidu instance (for MetroMan code to english name lookup)
	baidu_server, err := baidu_client.CreateServer()
	if err != nil {
		panic(err)
	}

	// Iterate over GTFS feeds
	zip_paths, err := filepath.Glob("build/*.gtfs.zip")
	if err != nil {
		log.Fatalf("glob error: %v", err)
	}
	if len(zip_paths) == 0 {
		log.Fatalf("no GTFS zip files in build/")
	}

	for _, zip_path := range zip_paths {
		fmt.Printf("=== Feed: %s ===\n", filepath.Base(zip_path))

		feed := gtfsparser.NewFeed()
		if err := feed.Parse(zip_path); err != nil {
			log.Printf("warning: failed to parse %s: %v", zip_path, err)
			continue
		}

		var agency *gtfs.Agency
		for _, a := range feed.Agencies {
			agency = a
		}
		if agency == nil {
			log.Printf("feed %s has no agency, skipping\n", zip_path)
			continue
		}

		city_code := agency.Id
		city_mapping, ok := baidu_server.CityUIDMappingsByMetromanCode[city_code]
		if !ok {
			log.Printf("no baidu mapping for metroman code %s, skipping\n", city_code)
			continue
		}
		english_city_name := city_mapping.EnglishName
		fmt.Printf("City: %s\n", english_city_name)

		// Collect only stops with coordinates
		stops := make([]*gtfs.Stop, 0, len(feed.Stops))
		for _, stop := range feed.Stops {
			if stop != nil && !(stop.Lat == 0 && stop.Lon == 0) {
				stops = append(stops, stop)
			}
		}
		if len(stops) < 2 {
			log.Printf("feed %s has <2 stops with coordinates, skipping", zip_path)
			continue
		}

		// Random route sampling
		for i := 0; i < 100; i++ {
			time.Sleep(time.Millisecond * 500)

			a_idx := rand.Intn(len(stops))
			b_idx := rand.Intn(len(stops) - 1)
			if b_idx >= a_idx {
				b_idx++
			}

			stop_a := stops[a_idx]
			stop_b := stops[b_idx]

			fmt.Printf("Pair %3d: %s (%s) → %s (%s)\n",
				i+1, stop_a.Name, stop_a.Id, stop_b.Name, stop_b.Id,
			)

			itinerary, err := queryOtpRoute(stop_a, stop_b)
			if err != nil {
				fmt.Printf("  OTP error: %v\n", err)
				continue
			}
			if itinerary == nil {
				fmt.Println("  No itinerary found.")
				continue
			}

			fmt.Printf("  Duration: %.0f sec\n", itinerary.Duration)
			fmt.Println("  Legs:")
			for j, leg := range itinerary.Legs {
				fmt.Printf("    %2d: %-8s %s → %s\n",
					j+1,
					leg.Mode,
					leg.From.Name,
					leg.To.Name,
				)
			}

			metroman_routes, err := getMetromanRoutes(english_city_name, stop_a.Name, stop_b.Name, time.Now())
			if err != nil {
				log.Printf("warning: failed to get metroman routes for %s to %s: %v", stop_a.Name, stop_b.Name, err)
				continue
			}

			spew.Dump(metroman_routes)
		}

		fmt.Println()
	}
}

func startOtpContainer(ctx context.Context) (*exec.Cmd, error) {
	cmd := exec.CommandContext(
		ctx,
		"docker", "run",
		"--rm",
		"-p", "8080:8080",
		"-v", "./build:/var/opentripplanner",
		// pin to a specific OTP version if you like, e.g. v2.7.0
		"docker.io/opentripplanner/opentripplanner:2.8.1",
		"--load",
		"--serve",
	)

	//cmd.Stdout = os.Stdout
	//cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Terminate docker when parent is terminated
	go func() { _ = cmd.Wait() }()

	return cmd, nil
}

// waitForOtp just waits until the server responds with any non-5xx code
func waitForOtp() error {
	client := &http.Client{Timeout: 2 * time.Second}
	url_str := "http://localhost:8080/otp"

	deadline := time.Now().Add(2 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for OTP server")
		}

		resp, err := client.Get(url_str)
		if err == nil && resp.StatusCode < 500 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}

		time.Sleep(3 * time.Second)
	}
}

// queryOtpRoute uses the OTP GTFS GraphQL API (transit-only)
func queryOtpRoute(stop_a, stop_b *gtfs.Stop) (*OtpItinerary, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	graphql_url := "http://localhost:8080/otp/gtfs/v1"

	query := `
query Plan(
  $fromLat: Float!,
  $fromLon: Float!,
  $toLat:   Float!,
  $toLon:   Float!,
  $date:    String!,
  $time:    String!
) {
  plan(
    from: { lat: $fromLat, lon: $fromLon }
    to:   { lat: $toLat,   lon: $toLon   }
    date: $date
    time: $time
    transportModes: [{ mode: TRANSIT }]
    numItineraries: 1
  ) {
    itineraries {
      duration
      legs {
        mode
        from { name }
        to   { name }
      }
    }
  }
}
`

	variables := map[string]interface{}{
		"fromLat": stop_a.Lat,
		"fromLon": stop_a.Lon,
		"toLat":   stop_b.Lat,
		"toLon":   stop_b.Lon,
		"date":    otp_date,
		"time":    otp_time,
	}

	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", graphql_url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OTP GraphQL HTTP %d: %s", resp.StatusCode, string(body))
	}

	var gql_resp OtpGraphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gql_resp); err != nil {
		return nil, err
	}

	if len(gql_resp.Errors) > 0 {
		return nil, fmt.Errorf("OTP GraphQL error: %s", gql_resp.Errors[0].Message)
	}
	if gql_resp.Data.Plan == nil || len(gql_resp.Data.Plan.Itineraries) == 0 {
		return nil, nil
	}

	return &gql_resp.Data.Plan.Itineraries[0], nil
}

// slugStationName converts a station name like
// "Shanghai Science & Technology Museum" → "shanghai-science-technology-museum".
func slugStationName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "&", "")
	fields := strings.Fields(name)
	return strings.Join(fields, "-")
}

// getMetromanRoutes fetches the planner page and returns all ride segments
// (each line you take) with start/end station names and on/off times.
func getMetromanRoutes(city, from_name, to_name string, dt time.Time) ([]RouteLeg, error) {
	city_slug := strings.ToLower(strings.ReplaceAll(city, " ", "-"))
	from_slug := slugStationName(from_name)
	to_slug := slugStationName(to_name)

	// datetime=YYYYMMDDHHMM
	datetime_str := dt.Format("200601021504")

	base_url := fmt.Sprintf(
		"https://www.metroman.cn/en/planner/%s/%s-to-%s",
		city_slug,
		from_slug,
		to_slug,
	)

	q := url.Values{}
	q.Set("mode", "depart")
	q.Set("datetime", datetime_str)

	full_url := base_url + "?" + q.Encode()

	resp, err := http.Get(full_url)
	if err != nil {
		return []RouteLeg{}, fmt.Errorf("fetch %s: %w", full_url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return []RouteLeg{}, fmt.Errorf("unexpected status %d from %s: %s",
			resp.StatusCode, full_url, string(b))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []RouteLeg{}, fmt.Errorf("read body: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return []RouteLeg{}, fmt.Errorf("parse HTML: %w", err)
	}

	card := doc.Find(".result-card").First()
	if card.Length() == 0 {
		return []RouteLeg{}, fmt.Errorf("no .result-card found")
	}

	return extractLegsFromResultCard(card), nil
}

// extractLegsFromResultCard walks the result-card structure and
// infers each ride segment (between transfers / final station).
func extractLegsFromResultCard(card *goquery.Selection) []RouteLeg {
	var legs []RouteLeg

	var current_line_name string
	var origin_name string
	var origin_time string

	// Iterate over direct children of .result-card in order
	card.Children().Each(func(_ int, s *goquery.Selection) {
		class_attr, _ := s.Attr("class")

		switch {
		case hasClass(class_attr, "result-card__station"):
			name := textTrim(s.Find(".result-card__station-info span").First().Text())
			time_txt := textTrim(s.Find(".result-card__station-time span").First().Text())

			if current_line_name == "" && origin_name == "" {
				// First station: initial origin
				origin_name = name
				origin_time = time_txt
			} else if current_line_name != "" && origin_name != "" {
				// This is a terminal station for the current line
				legs = append(legs, RouteLeg{
					LineName:   current_line_name,
					FromName:   origin_name,
					BoardTime:  origin_time,
					ToName:     name,
					AlightTime: time_txt,
				})
				// Route ends here in typical case
				origin_name = ""
				origin_time = ""
				current_line_name = ""
			}

		case hasClass(class_attr, "result-card__line"):
			// Set current line name
			line_name := textTrim(s.Find(".result-card__line-name").First().Text())
			if line_name != "" {
				current_line_name = line_name
			}

		case hasClass(class_attr, "result-card__transfer"):
			// Transfer: closes one leg and starts next one
			transfer_name := textTrim(s.Find(".result-card__transfer-info span").First().Text())
			time_spans := s.Find(".result-card__transfer-time span")

			if time_spans.Length() == 0 {
				return
			}

			arrival_time := textTrim(time_spans.First().Text())
			departure_time := arrival_time
			if time_spans.Length() >= 2 {
				departure_time = textTrim(time_spans.Last().Text())
			}

			if current_line_name != "" && origin_name != "" {
				legs = append(legs, RouteLeg{
					LineName:   current_line_name,
					FromName:   origin_name,
					BoardTime:  origin_time,
					ToName:     transfer_name,
					AlightTime: arrival_time,
				})
			}

			// Next leg starts from here
			origin_name = transfer_name
			origin_time = departure_time
			// current_line_name will be updated when the next .result-card__line appears
		}
	})

	return legs
}

// hasClass checks if the class attribute string contains a given class.
func hasClass(class_attr, class_name string) bool {
	for _, c := range strings.Fields(class_attr) {
		if c == class_name {
			return true
		}
	}
	return false
}

// textTrim normalizes and trims text.
func textTrim(s string) string {
	s = norm.NFKC.String(s)
	return strings.TrimSpace(s)
}
