package metroman_client

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
)

type MetromanServer struct {
	Cities        map[string]*MetromanCity
	ZipDateLookup map[string]string

	BaiduServer *baidu_client.BaiduServer
}

type MetromanDate struct {
	Year  int
	Month int
	Day   int
}

type MetromanSchedule struct {
	DaysOfWeek [7]int
	Holidays   bool
}

type MetromanCity struct {
	Lines  []*MetromanLine
	Routes []*MetromanRoute

	// Heuristic key for stations is SimplifiedName, will be experimenting
	Stations       []*MetromanStation
	StationsByName map[string]*MetromanStation
	StationsByCode map[string]*MetromanStation

	StationExitsByCode map[string][]*MetromanExit

	FareMatrices       []*[][]int
	FareMatrixStations [][]*MetromanStation

	Holidays    []MetromanDate
	ScheduleDef map[string]MetromanSchedule
}

type MetromanStation struct {
	Code  string
	Index int // Assigned by us

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

	Stations []*MetromanStation
}

type MetromanRoute struct {
	Code string

	EnglishName     string
	SimplifiedName  string
	TraditionalName string
	JapaneseName    string

	Stations []*MetromanStation
	Line     *MetromanLine
	Schedule MetromanSchedule
}

// Exits include toilets, may exclude outright for now
type MetromanExit struct {
	Code string

	SimplifiedName        string
	SimplifiedDescription string
}

// Used to get the combined schedules of routes
func OrSchedules(schedules []MetromanSchedule) MetromanSchedule {
	var out MetromanSchedule

	for _, s := range schedules {
		// OR each of the 7 days
		for i := 0; i < 7; i++ {
			if s.DaysOfWeek[i] == 1 {
				out.DaysOfWeek[i] = 1
			}
		}

		// OR holidays
		out.Holidays = out.Holidays || s.Holidays
	}

	return out
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

func (s *MetromanServer) SetBaiduServer(baidu_server *baidu_client.BaiduServer) {
	s.BaiduServer = baidu_server
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
		return nil, fmt.Errorf("could not open zip reader: %v", err)
	}

	lines := []*MetromanLine{}
	routes := []*MetromanRoute{}
	stations := []*MetromanStation{}
	stations_by_name := make(map[string]*MetromanStation)
	stations_by_code := make(map[string]*MetromanStation)
	lines_by_code := make(map[string]*MetromanLine)
	routes_by_code := make(map[string]*MetromanRoute)
	fare_matrices := []*[][]int{}
	fare_matrix_stations := [][]*MetromanStation{}
	holidays := []MetromanDate{}

	// Read in stations/lines first from uno.csv
	uno_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/uno.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open uno.csv: %v", err)
	}

	// Read through the CSV
	uno_csv_lines := strings.Split(string(uno_csv_contents), "\r\n")

	station_index := 0
	for _, uno_record_line := range uno_csv_lines {
		uno_record := strings.Split(uno_record_line, "<,>")

		if uno_record[1] == "MS" {
			lat_raw, _ := strconv.ParseFloat(uno_record[8], 64)
			lng_raw, _ := strconv.ParseFloat(uno_record[9], 64)
			subway_map_x, _ := strconv.ParseInt(uno_record[10], 10, 0)
			subway_map_y, _ := strconv.ParseInt(uno_record[11], 10, 0)
			station := MetromanStation{
				Code:             uno_record[0],
				Index:            station_index,
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

			stations = append(stations, &station)
			stations_by_name[station.SimplifiedName] = &station
			stations_by_code[station.Code] = &station

			station_index++
		}

		// ML is a metro line, WL is a walking line
		if uno_record[1] == "ML" || uno_record[1] == "WL" {
			line := MetromanLine{
				Code:            uno_record[0],
				EnglishName:     uno_record[2],
				SimplifiedName:  uno_record[3],
				TraditionalName: uno_record[4],
				JapaneseName:    uno_record[5],
				ShortName:       uno_record[7],
				Color:           uno_record[12],
				Stations:        []*MetromanStation{},
			}

			lines = append(lines, &line)
			lines_by_code[line.Code] = &line
		}

		// Metro route and miscellaneous routes (used to specify 2 distinct stations that are connected, hence free to travel between)
		if uno_record[1] == "MW" || uno_record[1] == "WW" {
			route := MetromanRoute{
				Code:            uno_record[0],
				EnglishName:     uno_record[2],
				SimplifiedName:  uno_record[3],
				TraditionalName: uno_record[4],
				JapaneseName:    uno_record[5],
			}

			routes = append(routes, &route)
			routes_by_code[route.Code] = &route
		}
	}

	// Read in stations in line from line.csv
	line_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/line.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open line.csv: %v", err)
	}

	// Read through the CSV
	line_csv_lines := strings.Split(string(line_csv_contents), "\r\n")

	// Add every station on the line to its list
	for _, line_record_line := range line_csv_lines {
		line_record := strings.Split(line_record_line, ",")

		line := lines_by_code[line_record[0]]
		for _, station_idx_str := range line_record[1:] {
			station_idx, _ := strconv.ParseInt(station_idx_str, 10, 0)
			line.Stations = append(line.Stations, stations[station_idx])
		}
	}

	// Read in stations in line from way.csv (the "line.csv" of routes)
	way_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/way.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open way.csv: %v", err)
	}

	// Read through the CSV
	way_csv_lines := strings.Split(string(way_csv_contents), "\r\n")

	// Add every station on the route to its list
	for _, way_record_line := range way_csv_lines {
		way_record := strings.Split(way_record_line, ",")

		route := routes_by_code[way_record[0]]

		for _, station_idx_str := range way_record[3:] {
			station_idx, _ := strconv.ParseInt(station_idx_str, 10, 0)
			route.Stations = append(route.Stations, stations[station_idx])
		}

		line_idx, _ := strconv.ParseInt(way_record[1], 10, 0)
		route.Line = lines[line_idx]
	}

	//spew.Dump(lines_by_code["BJMLSD"])

	// Read in stations/lines from fare.csv (and other files pulled in)
	fare_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/fare.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open fare.csv: %v", err)
	}

	// Read through the CSV
	fare_csv_lines := strings.Split(string(fare_csv_contents), "\r\n")

	for _, fare_record_line := range fare_csv_lines {
		fare_record := strings.Split(fare_record_line, ",")

		// Fares are assigned per route
		// TODO currently the lines you take do not factor into price
		// Every route can only be done with a specific set of lines
		// if this changes handle that

		station_codes := strings.Split(fare_record[4], "|")
		stations := []*MetromanStation{}
		if len(station_codes) == 1 && station_codes[0] == "" {
			// Get stations from route
			stations = routes_by_code[fare_record[1]].Stations
		} else {
			for _, station_code := range station_codes {
				stations = append(stations, stations_by_code[station_code])
			}
		}

		// Create 2D fare matrix. First index is start, second is end
		fare_matrix := [][]int{}

		if len(fare_record[3]) > 0 {
			fare_matrix, err = CSVToMatrixInt(payload_reader, fmt.Sprintf("%s/%s", zip_prefix, fare_record[3]))
			if err != nil {
				return nil, fmt.Errorf("could not open %s: %v", fare_record[3], err)
			}
		} else {
			// All the station pairs have the same fixed price
			fixed_price, _ := strconv.ParseInt(fare_record[2], 10, 0)

			for i := 0; i < len(stations); i++ {
				fare_matrix = append(fare_matrix, make([]int, len(stations)))
			}

			for x, _ := range stations {
				for y, _ := range stations {
					fare_matrix[x][y] = int(fixed_price)
				}
			}
		}

		//routes := strings.Split(fare_record[1], "|")

		fare_matrices = append(fare_matrices, &fare_matrix)
		fare_matrix_stations = append(fare_matrix_stations, stations)
	}

	// Read in holidays
	holiday_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/holiday.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open holiday.csv: %v", err)
	}

	// Read through the CSV
	holiday_csv_lines := strings.Split(string(holiday_csv_contents), "\r\n")

	// Add every station on the route to its list
	for _, holiday_record_line := range holiday_csv_lines {
		// Just 1 column
		year, _ := strconv.ParseInt(holiday_record_line[0:4], 10, 0)
		month, _ := strconv.ParseInt(holiday_record_line[4:6], 10, 0)
		day, _ := strconv.ParseInt(holiday_record_line[6:8], 10, 0)
		holidays = append(holidays, MetromanDate{
			Year:  int(year),
			Month: int(month),
			Day:   int(day),
		})
	}

	schedule_def := make(map[string]MetromanSchedule)

	// Read in schedule definitions
	schedule_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/schedule.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open schedule.csv: %v", err)
	}

	// Read through the CSV
	schedule_csv_lines := strings.Split(string(schedule_csv_contents), "\r\n")

	// Add the schedules for each route
	for _, schedule_record_line := range schedule_csv_lines {
		schedule_record := strings.Split(schedule_record_line, "<,>")

		schedule_bits := [7]int{}
		for i, bit_str := range schedule_record[1:8] {
			if bit_str[0] == '1' {
				schedule_bits[i] = 1
			} else {
				schedule_bits[i] = 0
			}
		}

		// TODO whether weekdays-friday and friday are included is also specified
		include_holidays := false
		if schedule_record[9][0] == '1' {
			include_holidays = true
		}

		schedule_def[schedule_record[0]] = MetromanSchedule{
			DaysOfWeek: schedule_bits,
			Holidays:   include_holidays,
		}
	}

	// Read in schedules for routes
	wayschedule_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/wayschedule.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open wayschedule.csv: %v", err)
	}

	// Read through the CSV
	wayschedule_csv_lines := strings.Split(string(wayschedule_csv_contents), "\r\n")

	// Add the schedules for each route
	for _, wayschedule_record_line := range wayschedule_csv_lines {
		wayschedule_record := strings.Split(wayschedule_record_line, ",")

		schedules := []MetromanSchedule{}
		for _, schedule_code := range wayschedule_record[2:] {
			schedules = append(schedules, schedule_def[schedule_code])
		}

		// Combine all schedules together
		routes_by_code[wayschedule_record[0]].Schedule = OrSchedules(schedules)
	}

	return &MetromanCity{
		Lines:              lines,
		Routes:             routes,
		Stations:           stations,
		StationsByName:     stations_by_name,
		StationsByCode:     stations_by_code,
		FareMatrices:       fare_matrices,
		FareMatrixStations: fare_matrix_stations,
		Holidays:           holidays,
		ScheduleDef:        schedule_def,
	}, nil
}

func CSVToMatrixInt(payload_reader *zip.Reader, filename string) ([][]int, error) {
	matrix_csv_contents, err := ReadFileFromCSV(payload_reader, filename)
	if err != nil {
		return nil, err
	}

	// Read through the CSV
	matrix_csv_lines := strings.Split(string(matrix_csv_contents), "\r\n")
	output_matrix := [][]int{}
	for _, matrix_record_line := range matrix_csv_lines {
		matrix_record := strings.Split(matrix_record_line, ",")

		output_line := make([]int, len(matrix_record))
		for i, elem := range matrix_record {
			elem_64, _ := strconv.ParseInt(elem, 10, 0)
			output_line[i] = int(elem_64)
		}

		output_matrix = append(output_matrix, output_line)
	}

	return output_matrix, nil
}

func (s *MetromanServer) GenerateStopsTXT(code string, full bool) (string, error) {
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

		url := ""
		use_autocomplete_fallback := false

		if full {
			autocomplete, err := s.BaiduServer.GetAutocomplete(code, station.SimplifiedName)
			if err != nil {
				use_autocomplete_fallback = true
			} else {
				entry, found := baidu_client.GetAutocompleteStation(autocomplete)
				if !found {
					use_autocomplete_fallback = true
				} else {
					url = fmt.Sprintf(
						"https://map.baidu.com/poi//@0,0?uid=%s&info_merge=1&isBizPoi=false&ugc_type=3&ugc_ver=1&device_ratio=2&compat=1&pcevaname=pc4.1&querytype=detailConInfo&da_src=shareurl", entry.UID)
				}
			}
		}

		if use_autocomplete_fallback {
			fmt.Printf("Try typing autocomplete fallback for %s\n", station.EnglishName)

			// Try typing autocomplete, uses a heuristic
			autocomplete_typing, err := s.BaiduServer.GetAutocompleteType(station.SimplifiedName)
			if err != nil {
				return "", fmt.Errorf("could not get autocomplete type from baidu for \"%s\": %v", station.SimplifiedName, err)
			}

			station_uid, found := baidu_client.GetAutocompleteTypeStation(autocomplete_typing)
			if !found {
				return "", fmt.Errorf("could not get station from either autocomplete approach for \"%s\"", station.SimplifiedName)
			} else {
				url = fmt.Sprintf(
					"https://map.baidu.com/poi//@0,0?uid=%s&info_merge=1&isBizPoi=false&ugc_type=3&ugc_ver=1&device_ratio=2&compat=1&pcevaname=pc4.1&querytype=detailConInfo&da_src=shareurl", station_uid)
			}
		}

		output = append(output, fmt.Sprintf("%s,%s,%s,,,%f,%f,%s,%s,%d,,%s,%d,,,",
			station_code,           // Potentially internal to MetroMan
			station.SimplifiedName, // Potentially not true for cities other than Beijing
			station.EnglishName,    // China likely defaults to the Chinese name for all public comms, we'll use English for now though
			corrected_coord.Lat,
			corrected_coord.Lng,
			fmt.Sprintf("zone_%s", station_code), // Peculiarly of GTFS: fares cannot be specified by distance, this must be done instead
			url,
			1,
			"Asia/Shanghai",
			0,
		))

		fmt.Printf("Handled row for %s\n", station.EnglishName)
	}

	return strings.Join(output, "\n"), nil
}

func (s *MetromanServer) GenerateFaresTXT(code string, full bool) (string, string, error) {
	city, exists := s.Cities[code]
	if !exists {
		return "", "", fmt.Errorf("city %v not loaded", code)
	}

	// You pay 3 generally if you enter and exit the subway: https://www.reddit.com/r/shanghai/comments/18lrq74/enter_exit_same_metro_station_what_happens/

	// Will be using GTFS fares v1 and generating every station to station pair in one direction
	// TODO only do one direction optimization if the fare matrix is mirrored, for now assuming it

	// First generate fare_rules.txt
	rules_output := []string{
		"fare_id,route_id,origin_id,destination_id,contains_id",
	}
	attributes_output := []string{
		"fare_id,price,currency_type,payment_method,transfers,agency_id,transfer_duration",
	}

	for i, fare_matrix_stations := range city.FareMatrixStations {
		for x, start_station := range fare_matrix_stations {
			for y, end_station := range fare_matrix_stations {
				// I am allowing ALL station pairs so transit apps don't choke
				// if end_station.Index >= start_station.Index

				rules_output = append(rules_output, fmt.Sprintf("fare_%s_%s,,zone_%s,zone_%s,",
					city.Stations[start_station.Index].Code,
					city.Stations[end_station.Index].Code,
					city.Stations[start_station.Index].Code,
					city.Stations[end_station.Index].Code,
				))

				attributes_output = append(attributes_output, fmt.Sprintf("fare_%s_%s,%d,%s,%d,%d,,",
					city.Stations[start_station.Index].Code,
					city.Stations[end_station.Index].Code,
					(*city.FareMatrices[i])[x][y],
					"CNY",
					1,
					0,
				))
			}
		}
	}

	return strings.Join(rules_output, "\n"),
		strings.Join(attributes_output, "\n"), nil
}

func (s *MetromanServer) GenerateAgencyTXT(code string) string {
	output := []string{
		"agency_id,agency_name,agency_url,agency_timezone,agency_lang,agency_phone",
		fmt.Sprintf("%s,MetroMan-GTFS %s,,%s,%s,", code, code, "Asia/Shanghai", "zh"),
	}

	return strings.Join(output, "\n")
}

func (s *MetromanServer) GenerateRoutesTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	output := []string{
		"agency_id,route_id,route_short_name,route_long_name,route_type,route_url,route_color,route_text_color",
	}

	for _, route := range city.Routes {
		output = append(output, fmt.Sprintf("%s,%s,,%s,%d,%s,%s,%s",
			city_code,
			route.Code,
			route.EnglishName,
			2,  // https://gtfs.org/documentation/schedule/reference/#routestxt
			"", // No URL YET
			route.Line.Color,
			"#000000",
		))
	}

	return strings.Join(output, "\n"), nil
}

func (s *MetromanServer) GenerateCalendarTXT(city_code string) (string, string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", "", fmt.Errorf("city %v not loaded", city_code)
	}

	calendar_output := []string{
		"service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date",
	}
	calendar_dates_output := []string{
		"service_id,date,exception_type",
	}

	for schedule_code, schedule := range city.ScheduleDef {
		any_day_of_week_set := schedule.DaysOfWeek[0] == 1 || schedule.DaysOfWeek[1] == 1 || schedule.DaysOfWeek[2] == 1 || schedule.DaysOfWeek[3] == 1 || schedule.DaysOfWeek[4] == 1 || schedule.DaysOfWeek[5] == 1 || schedule.DaysOfWeek[6] == 1

		// A day of the week must be specified or this must have holidays set (as holidays must still reference a schedule)
		if any_day_of_week_set || schedule.Holidays {
			calendar_output = append(calendar_output, fmt.Sprintf("%s,%d,%d,%d,%d,%d,%d,%d,%s,%s",
				schedule_code,
				schedule.DaysOfWeek[0],
				schedule.DaysOfWeek[1],
				schedule.DaysOfWeek[2],
				schedule.DaysOfWeek[3],
				schedule.DaysOfWeek[4],
				schedule.DaysOfWeek[5],
				schedule.DaysOfWeek[6],
				fmt.Sprintf("%04d%02d%02d", 2000, 1, 1),   // Day in the past
				fmt.Sprintf("%04d%02d%02d", 9999, 12, 31), // Day in the future
			))
		}

		date_action := 2 // Remove the date
		if schedule.Holidays {
			date_action = 1 // Add the date
		}

		// Note every single holiday day
		for _, holiday := range city.Holidays {
			calendar_dates_output = append(calendar_dates_output, fmt.Sprintf("%s,%s,%d",
				schedule_code,
				fmt.Sprintf("%04d%02d%02d", holiday.Year, holiday.Month, holiday.Day),
				date_action, // Whether this date was added or not depends on the holiday
			))
		}
	}

	return strings.Join(calendar_output, "\n"), strings.Join(calendar_dates_output, "\n"), nil
}
