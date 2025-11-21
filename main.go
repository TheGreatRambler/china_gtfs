package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"tgrcode.com/baidu_client"

	"honnef.co/go/spew"
)

type MetromanServer struct {
	Cities        map[string]*MetromanCity
	ZipDateLookup map[string]string
}

type MetromanCity struct {
	Lines  []MetromanLine
	Routes []MetromanRoute

	// Heuristic key for stations is SimplifiedName, will be experimenting
	StationsByName map[string]MetromanStation
	StationsByCode map[string]MetromanStation

	StationExitsByCode map[string][]MetromanExit
}

type MetromanStation struct {
	Code string

	EnglishName      string
	SimplifiedName   string
	TraditionalName  string
	JapaneseName     string
	EnglishShortName string

	ShortName string

	Lat float64
	Lng float64

	SubwayMapX int
	SubwayMapY int
}

type MetromanLine struct {
	Code string

	EnglishName     string
	SimplifiedName  string
	TraditionalName string
	JapaneseName    string

	ShortName string

	Color string
}

type MetromanRoute struct {
	Code string

	EnglishName     string
	SimplifiedName  string
	TraditionalName string
	JapaneseName    string
}

// Exits include toilets, may exclude outright for now
type MetromanExit struct {
	Code string

	SimplifiedName        string
	SimplifiedDescription string
}

func ReadFileFromCSV(zip_reader *zip.Reader, name string) ([]byte, error) {
	var chosen_file *zip.File
	for _, file := range zip_reader.File {
		if file.Name == name {
			chosen_file = file
			break
		}
	}

	opened_file, err := chosen_file.Open()
	if err != nil {
		return nil, err
	}

	contents, err := io.ReadAll(opened_file)
	if err != nil {
		return nil, err
	}

	return contents, nil
}

func CreateServer() (*MetromanServer, error) {
	// Download version.txt (without headers)
	// Determined with a reverse proxy
	versions_resp, err := http.Get("https://metroman.oss-cn-hangzhou.aliyuncs.com/app/metromanandroid/v202005/version.txt")
	if err != nil {
		return nil, err
	}
	defer versions_resp.Body.Close()

	reader := csv.NewReader(versions_resp.Body)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	// Create versions lookup
	versions_lookup := make(map[string]string)
	for _, record := range records {
		if len(record) == 3 {
			versions_lookup[record[0]] = record[1]
		}
	}

	return &MetromanServer{
		Cities:        make(map[string]*MetromanCity),
		ZipDateLookup: versions_lookup,
	}, nil
}

func (s *MetromanServer) LoadCity(code string) error {
	// Get zip date, erroring if this city does not exist
	zip_date, ok := s.ZipDateLookup[code]
	if !ok {
		return fmt.Errorf("city with code '%s' does not exist", code)
	}

	// Download zip (without headers)
	zip_resp, err := http.Get(fmt.Sprintf("https://metroman.oss-cn-hangzhou.aliyuncs.com/app/metromanandroid/v202005/%s/%s.zip", code, zip_date))
	if err != nil {
		return err
	}
	defer zip_resp.Body.Close()

	zip, err := io.ReadAll(zip_resp.Body)
	if err != nil {
		return err
	}
	// Load this zip now
	city, err := LoadCity(zip_date, zip)
	if err != nil {
		return err
	}

	// Add to our map
	s.Cities[code] = city

	return nil
}

func LoadCity(zip_prefix string, payload []byte) (*MetromanCity, error) {
	// Step 1: Create a new zip reader from the []byte data
	payload_reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, err
	}

	lines := []MetromanLine{}
	routes := []MetromanRoute{}
	stations_by_name := make(map[string]MetromanStation)
	stations_by_code := make(map[string]MetromanStation)

	// Read in stations/lines first from uno.csv
	uno_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/uno.csv", zip_prefix))
	if err != nil {
		return nil, err
	}

	// Read through the CSV
	uno_csv_lines := strings.Split(string(uno_csv_contents), "\r\n")

	for _, uno_record_line := range uno_csv_lines {
		uno_record := strings.Split(uno_record_line, "<,>")

		if uno_record[1] == "MS" {
			lat_raw, _ := strconv.ParseFloat(uno_record[8], 64)
			lng_raw, _ := strconv.ParseFloat(uno_record[9], 64)
			subway_map_x, _ := strconv.ParseInt(uno_record[10], 10, 32)
			subway_map_y, _ := strconv.ParseInt(uno_record[11], 10, 32)
			station := MetromanStation{
				Code:             uno_record[0],
				EnglishName:      uno_record[2],
				SimplifiedName:   uno_record[3],
				TraditionalName:  uno_record[4],
				JapaneseName:     uno_record[5],
				EnglishShortName: uno_record[6],
				ShortName:        uno_record[7],
				Lat:              lat_raw,
				Lng:              lng_raw,
				SubwayMapX:       int(subway_map_x),
				SubwayMapY:       int(subway_map_y),
			}

			stations_by_name[station.SimplifiedName] = station
			stations_by_code[station.Code] = station
		}

		if uno_record[1] == "ML" {
			line := MetromanLine{
				Code:            uno_record[0],
				EnglishName:     uno_record[2],
				SimplifiedName:  uno_record[3],
				TraditionalName: uno_record[4],
				JapaneseName:    uno_record[5],
				ShortName:       uno_record[7],
				Color:           uno_record[12],
			}

			lines = append(lines, line)
		}

		if uno_record[1] == "MW" {
			route := MetromanRoute{
				Code:            uno_record[0],
				EnglishName:     uno_record[2],
				SimplifiedName:  uno_record[3],
				TraditionalName: uno_record[4],
				JapaneseName:    uno_record[5],
			}

			routes = append(routes, route)
		}
	}

	/*
		// Heuristic to get the likely city prefix (usually just first 3 chars)
		city_prefix := lines[0].Info.Code[0 : len(lines[0].Info.Code)-3]

		// Read in what stations comprise a line
		line_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/line.csv", zip_prefix))
		if err != nil {
			return nil, err
		}

		// Read through the CSV
		line_csv_lines := strings.Split(string(line_csv_contents), "\r\n")
		for line_index, line_record_line := range line_csv_lines {
			// NOTE The line index has the potential to go farther than expected
			// Appears to be for lines labeled "W", could be monorails or similar
			// Check later when relevant
			if line_index >= len(lines) {
				break
			}

			line_record := strings.Split(line_record_line, ",")

			// First element is line name (guaranteed to be same order as uno.csv so we don't care)
			for i := 1; i < len(line_record); i++ {
				// Get station index as number
				station_index, err := strconv.Atoi(line_record[i])
				if err != nil {
					return nil, err
				}

				// Construct station code, should always be 3 digit code (tested with Shanghai)
				station_code := fmt.Sprintf("%sS%03d", city_prefix, station_index+1)
				station, ok := stations_by_code[station_code]
				if !ok {
					return nil, fmt.Errorf("could not find station listed in line.csv")
				}

				// Append this station to line
				lines[line_index].Stations = append(lines[line_index].Stations, station)
				lines[line_index].StationIndex[station_code] = len(lines[line_index].Stations) - 1
			}
		}

		// Read in what schedules comprise a line
		schedule_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/wayschedule.csv", zip_prefix))
		if err != nil {
			return nil, err
		}

		// Read what schedules comprise a line
		schedule_csv_lines := strings.Split(string(schedule_csv_contents), "\r\n")
		schedule_line_index := 0
		for _, schedule_record_line := range schedule_csv_lines {
			schedule_record := strings.Split(schedule_record_line, ",")

			// Only include the 'A' lines, once again just for now
			if (schedule_record[0][len(schedule_record[0])-1]) == 'A' {
				if schedule_line_index >= len(lines) {
					break
				}

				lines[schedule_line_index].Schedules = schedule_record[2:]

				schedule_line_index++
			}
		}

		// Add down and up times to line
		for i, line := range lines {
			// Some lines only go one direction, like BJMWSDA (Capital Airport Express) (Beijing)
			// Handle that by ignoring errors here
			down_times, err := HandleStationTimes(zip_prefix, payload_reader, line.Down.Code)
			if err != nil {
				return nil, err
			}

			if len(down_times) > 0 {
				// Split down the middle for weekday and holiday (presumed split)
				lines[i].WeekdayDownTimes = down_times[:len(down_times)/len(line.Schedules)]
				//lines[i].HolidayDownTimes = down_times[len(down_times)/2:]

				// Calculate when trains were added
				lines[i].WeekdayDownTimesAddedTrain = GetTrainsAdded(lines[i].WeekdayDownTimes, false)
				//lines[i].HolidayDownTimesAddedTrain = GetTrainsAdded(lines[i].HolidayDownTimes, false)
			}

			up_times, err := HandleStationTimes(zip_prefix, payload_reader, line.Up.Code)
			if err != nil {
				return nil, err
			}

			if len(up_times) > 0 {
				// Split down the middle for weekday and holiday (presumed split)
				lines[i].WeekdayUpTimes = up_times[:len(up_times)/len(line.Schedules)]
				//lines[i].HolidayUpTimes = up_times[len(up_times)/2:]

				// Calculate when trains were added
				lines[i].WeekdayUpTimesAddedTrain = GetTrainsAdded(lines[i].WeekdayUpTimes, true)
				//lines[i].HolidayUpTimesAddedTrain = GetTrainsAdded(lines[i].HolidayUpTimes, true)
			}
		}
	*/

	return &MetromanCity{
		Lines:          lines,
		Routes:         routes,
		StationsByName: stations_by_name,
		StationsByCode: stations_by_code,
	}, nil
}

func (s *MetromanServer) GenerateStopsTXT(code string) (string, error) {
	city, exists := s.Cities[code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", code)
	}

	output := []string{
		"stop_id,stop_code,stop_name,tts_stop_name,stop_desc," +
			"stop_lat,stop_lon,zone_id,stop_url,location_type," +
			"parent_station,stop_timezone,wheelchair_boarding," +
			"level_id,platform_code,stop_access",
	}

	for station_code, station := range city.StationsByCode {
		corrected_coord := GCJ02ToWGS84(Coordinate{
			Lat: station.Lat,
			Lng: station.Lng,
		})

		output = append(output, fmt.Sprintf("%s,%s,%s,,,%f,%f,,",
			station_code,           // Potentially internal to MetroMan
			station.SimplifiedName, // Potentially not true for cities other than Beijing
			station.SimplifiedName, // China likely defaults to the Chinese name for all public comms
			corrected_coord.Lat,
			corrected_coord.Lng,
		))
	}

	return strings.Join(output, "\n"), nil
}

type MetromanDirection int

const (
	DOWN MetromanDirection = iota
	UP
)

func (s *MetromanServer) CleanLineName(input_line_name string) string {
	// Have to remove redundant "地铁"
	input_line_name = strings.ReplaceAll(input_line_name, "地铁", "")

	// Have to remove redundant "轨道交通"
	input_line_name = strings.ReplaceAll(input_line_name, "轨道交通", "")

	// if I find this ("line" in Chinese), just use it
	line_index := strings.Index(input_line_name, "号线")
	if line_index == -1 {
		// Need to clean line name for MetroMan, usually just means exclude everything after "("
		parenthese_index := strings.Index(input_line_name, "(")
		if parenthese_index == -1 {
			return input_line_name
		} else {
			return input_line_name[:parenthese_index]
		}
	} else {
		// Include the "号线"
		return input_line_name[:line_index+len([]byte("号线"))]
	}
}

func main() {
	/*
		metroman_server, err := CreateServer()
		if err != nil {
			panic(err)
		}
	*/

	baidu_server, err := baidu_client.CreateServer()
	if err != nil {
		panic(err)
	}

	autocomplete_responses, err := baidu_server.GetAutocomplete("惠新西街北口")
	if err != nil {
		panic(err)
	}
	spew.Dump(autocomplete_responses)

	/*
		err = metroman_server.LoadCity("bj")
		if err != nil {
			panic(err)
		}

		spew.Dump(metroman_server)
	*/
}
