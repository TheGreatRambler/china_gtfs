package metroman_client

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"slices"
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
	Code       string
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
	ScheduleDef map[string]*MetromanSchedule
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
	// Just a simple lookup table for paths between stations
	StationPaths map[string][]Coordinate
}

type MetromanRoute struct {
	Code string

	EnglishName     string
	SimplifiedName  string
	TraditionalName string
	JapaneseName    string

	Stations               []*MetromanStation
	StationToScheduleIndex map[int]int // Station index -> index within schedule (schedule is not in order, is actually in sorted order)
	Line                   *MetromanLine
	IdxWithinLine          int
	Schedules              []*MetromanSchedule
	Trips                  [][]MetromanTrip // Set of trips for each schedule
}

type MetromanTrip struct {
	TripEnded bool
	Visits    []MetromanStationVisit
}

type MetromanStationVisit struct {
	Station                 *MetromanStation
	ArrivalAndDepartMinutes int
	//NextArrivalMinutes int
}

// Exits include toilets, may exclude outright for now
type MetromanExit struct {
	Code string

	SimplifiedName        string
	SimplifiedDescription string
}

// Used to get the combined schedules of routes
/*
func OrSchedules(schedules []MetromanSchedule) MetromanSchedule {
	var out MetromanSchedule

	all_codes := []string{}
	for _, s := range schedules {
		// OR each of the 7 days
		for i := 0; i < 7; i++ {
			if s.DaysOfWeek[i] == 1 {
				out.DaysOfWeek[i] = 1
			}
		}

		// OR holidays
		out.Holidays = out.Holidays || s.Holidays

		all_codes = append(all_codes, s.Code)
	}

	out.Code = strings.Join(all_codes, "_")

	return out
}
*/

func ReadFileFromCSV(zip_reader *zip.Reader, name string) ([]byte, error) {
	var chosen_file *zip.File
	for _, file := range zip_reader.File {
		if file.Name == name {
			chosen_file = file
			break
		}
	}

	if chosen_file == nil {
		return []byte{}, fmt.Errorf("could not find file %s in zip", name)
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

			corrected_coord := GCJ02ToWGS84(Coordinate{
				Lat: lat_raw,
				Lng: lng_raw,
			})

			station := MetromanStation{
				Code:             uno_record[0],
				Index:            station_index,
				EnglishName:      uno_record[2],
				SimplifiedName:   uno_record[3],
				TraditionalName:  uno_record[4],
				JapaneseName:     uno_record[5],
				EnglishShortName: uno_record[6],
				ShortName:        uno_record[7],
				Lat:              corrected_coord.Lat,
				Lng:              corrected_coord.Lng,
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
				StationPaths:    map[string][]Coordinate{},
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
	within_line_idx := map[string]int{}
	for _, way_record_line := range way_csv_lines {
		way_record := strings.Split(way_record_line, ",")

		route := routes_by_code[way_record[0]]

		for _, station_idx_str := range way_record[3:] {
			station_idx, _ := strconv.ParseInt(station_idx_str, 10, 0)
			route.Stations = append(route.Stations, stations[station_idx])
		}

		// Create a mapping so we can create the schedule later
		station_indices := []int{}
		// Exclude the last station in the route. This station is ignored in the schedule, the station before it denotes the arrival
		for _, station := range route.Stations[:len(route.Stations)-1] {
			station_indices = append(station_indices, station.Index)
		}
		slices.Sort(station_indices)
		station_to_schedule_idx := map[int]int{}
		for i, station_idx := range station_indices {
			station_to_schedule_idx[station_idx] = i
		}
		route.StationToScheduleIndex = station_to_schedule_idx

		line_idx, _ := strconv.ParseInt(way_record[1], 10, 0)
		route.Line = lines[line_idx]
		route.IdxWithinLine = within_line_idx[route.Line.Code]
		within_line_idx[route.Line.Code]++
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

	schedule_def := make(map[string]*MetromanSchedule)

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

		schedule_def[schedule_record[0]] = &MetromanSchedule{
			Code:       schedule_record[0],
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

		schedules := []*MetromanSchedule{}
		for _, schedule_code := range wayschedule_record[2:] {
			schedules = append(schedules, schedule_def[schedule_code])
		}

		routes_by_code[wayschedule_record[0]].Schedules = schedules
	}

	// Read in schedules for every route
	for _, route := range routes {
		//if route.Code == "BJMW04A" {
		//	spew.Dump(route.Stations)
		//}

		// Read in visit times for route
		schedule_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/%s.csv", zip_prefix, route.Code))
		if err != nil {
			// Some files like the walking routes don't exist, just ignore
			continue
		}

		// Read through the CSV
		schedule_csv_lines := strings.Split(string(schedule_csv_contents), "\r\n")

		// The format is as thus:
		//     The numbers will be increasing until a certain point,
		//         at which point they return to close to the beginning
		//     That block of times represents all of the departure,arrival pairs of the first and second station
		//     It seems safe to assume atm that the arrival and departure times of a
		//         particular station are always the same
		//     IMPORTANT: Subsequent stations may add their own trips to the route, meaning there are more trips
		//         than just those starting at the first station

		trips_by_schedule := [][]MetromanTrip{}
		station_arrivals_departures := [][]map[int]int{}

		// Will read file into here then parse
		// map is arrival_next -> departure
		current_station_arrivals_departures := []map[int]int{make(map[int]int)}
		station_idx := 0
		last_depart_min := 0
		for schedule_record_line_idx, schedule_record_line := range schedule_csv_lines {
			schedule_record := strings.Split(schedule_record_line, ",")

			depart_min, _ := strconv.ParseInt(schedule_record[0], 10, 0)
			arrive_next_min, _ := strconv.ParseInt(schedule_record[1], 10, 0)

			if int(depart_min) < last_depart_min {
				if len(current_station_arrivals_departures) == len(route.Stations)-1 {
					// A set of trips exists for this schedule. We have reached a new schedule
					// Save this one, clear and reset
					station_arrivals_departures = append(station_arrivals_departures,
						current_station_arrivals_departures)

					station_idx = 0
					current_station_arrivals_departures = []map[int]int{make(map[int]int)}
				} else {
					station_idx++
					// Always add to the list. This list is wiped so that there are never any extra maps
					current_station_arrivals_departures = append(current_station_arrivals_departures,
						make(map[int]int))
				}
			}

			current_station_arrivals_departures[station_idx][int(arrive_next_min)] = int(depart_min)
			last_depart_min = int(depart_min)

			// If we're the last one add the current map
			if schedule_record_line_idx == len(schedule_csv_lines)-1 {
				station_arrivals_departures = append(station_arrivals_departures,
					current_station_arrivals_departures)
			}
		}

		// Format has routes going the opposite direction reversed in the file format
		// Apply this change for every schedule
		//spew.Dump(route.EnglishName, route.IdxWithinLine%2)
		//if route.IdxWithinLine%2 == 1 {
		//	for schedule_idx, _ := range station_arrivals_departures {
		//		slices.Reverse(station_arrivals_departures[schedule_idx])
		//	}
		//}

		//for schedule_idx, _ := range station_arrivals_departures {
		//	spew.Dump(len(station_arrivals_departures[schedule_idx]), route.EnglishName)
		//}

		//if route.Code == "BJMW04A" {
		//	data, _ := json.MarshalIndent(station_arrivals_departures, "", "  ")
		//	os.WriteFile("route_BJMW04A_groups.json", data, 0644)
		//}

		for _, schedule_station_arrivals_departures := range station_arrivals_departures {
			// Now determine trips from this
			trips := []MetromanTrip{}
			arrival_trip_assigned := make(map[int]int)      // Arrival at next station to trip index (the trips array above)
			last_arrival_trip_assigned := make(map[int]int) // Same as above but for the last station. Will rotate these out and clear current at the end of each station

			// Iterate over stations rather than the schedule itself, will look up schedule instead
			for station_i := range len(route.Stations) - 1 {
				// Look up schedule
				this_arrivals_departures := schedule_station_arrivals_departures[route.StationToScheduleIndex[route.Stations[station_i].Index]]

				// Sweep over trips and note which are blacklisted (they have ended)
				trip_ended := make(map[int]bool)
				for trip_idx, trip := range trips {
					trip_ended[trip_idx] = trip.TripEnded
				}

				// Now tentatively note them as ended. This flag will be reverted if that is not true
				for _, trip := range trips {
					trip.TripEnded = true
				}

				if station_i == 0 {
					// We are going to iterate over the map. Not ordered, but doesn't matter too much
					for arrival_next_min, depart_min := range this_arrivals_departures {
						// Always creates new trips
						// We will create two stops here so we can cover the last station should it not be included
						trips = append(trips, MetromanTrip{
							TripEnded: false,
							Visits: []MetromanStationVisit{{
								Station:                 route.Stations[0],
								ArrivalAndDepartMinutes: depart_min,
							}, {
								Station:                 route.Stations[1],
								ArrivalAndDepartMinutes: arrival_next_min,
							}},
						})

						// Lookup for the next station
						arrival_trip_assigned[arrival_next_min] = len(trips) - 1
					}
				} else {
					// Rotate trip assigned map and clear current
					last_arrival_trip_assigned = arrival_trip_assigned
					arrival_trip_assigned = make(map[int]int)

					for arrival_next_min, depart_min := range this_arrivals_departures {
						trip_idx, trip_found := last_arrival_trip_assigned[depart_min]
						if trip_found && !trip_ended[trip_idx] {
							// Add to existing trip
							// NOTE we add the next station after this current one, not the current one. It already exists
							trips[trip_idx].Visits = append(trips[trip_idx].Visits, MetromanStationVisit{
								Station:                 route.Stations[station_i+1],
								ArrivalAndDepartMinutes: arrival_next_min,
							})
							trips[trip_idx].TripEnded = false

							// Note down the trip index again
							arrival_trip_assigned[arrival_next_min] = trip_idx
						} else {
							// Need to create a new trip
							trips = append(trips, MetromanTrip{
								TripEnded: false,
								Visits: []MetromanStationVisit{{
									Station:                 route.Stations[station_i],
									ArrivalAndDepartMinutes: depart_min,
								}, {
									Station:                 route.Stations[station_i+1],
									ArrivalAndDepartMinutes: arrival_next_min,
								}},
							})

							// Lookup for the next station
							arrival_trip_assigned[arrival_next_min] = len(trips) - 1
							//trip_idx = len(trips) - 1
						}
					}
				}
			}

			trips_by_schedule = append(trips_by_schedule, trips)
		}

		// Finally add it
		route.Trips = trips_by_schedule
	}

	// Read in the coords for lines in their entirety
	path_latlng_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/path_latlng.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open path_latlng.csv: %v", err)
	}

	// Read through the CSV
	path_latlng_csv_lines := strings.Split(string(path_latlng_csv_contents), "\r\n")

	all_latlng_coords := []Coordinate{}
	for _, path_latlng_record_line := range path_latlng_csv_lines {
		path_latlng_record := strings.Split(path_latlng_record_line, ",")

		lat_raw, _ := strconv.ParseFloat(path_latlng_record[0], 64)
		lng_raw, _ := strconv.ParseFloat(path_latlng_record[1], 64)

		// Add a new coord
		all_latlng_coords = append(all_latlng_coords, GCJ02ToWGS84(Coordinate{
			Lat: lat_raw,
			Lng: lng_raw,
		}))
	}

	// Read in the mappings for line and stations to their indices (inclusive) in the list of coords
	path_rail_csv_contents, err := ReadFileFromCSV(payload_reader, fmt.Sprintf("%s/path_rail.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open path_rail.csv: %v", err)
	}

	// Read through the CSV
	path_rail_csv_lines := strings.Split(string(path_rail_csv_contents), "\r\n")
	for _, path_rail_record_line := range path_rail_csv_lines {
		path_rail_record := strings.Split(path_rail_record_line, ",")

		lower, _ := strconv.ParseInt(path_rail_record[3], 10, 0)
		upper, _ := strconv.ParseInt(path_rail_record[4], 10, 0)

		line := lines_by_code[path_rail_record[0]]
		path_code := fmt.Sprintf("%s_%s", path_rail_record[1], path_rail_record[2])

		_, exists := line.StationPaths[path_code]
		if !exists {
			line.StationPaths[path_code] = []Coordinate{}
		}
		// Set the coords
		// TODO use a global list of coords and just index into it
		line.StationPaths[path_code] = all_latlng_coords[lower : upper+1]
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
			station.Lat,
			station.Lng,
			fmt.Sprintf("zone_%s", station_code), // Peculiarly of GTFS: fares cannot be specified by distance, this must be done instead
			url,
			0,
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
		fmt.Sprintf("%s,MetroMan-GTFS %s,%s,%s,%s,", code, code, "https://tgrcode.com/", "Asia/Shanghai", "zh"),
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
		// No hashtag in color
		color := ""
		if len(route.Line.Color) > 0 {
			color = route.Line.Color[1:]
		}

		output = append(output, fmt.Sprintf("%s,%s,%s,%s,%d,%s,%s,%s",
			city_code,
			route.Code,
			route.SimplifiedName,
			route.EnglishName,
			2,  // https://gtfs.org/documentation/schedule/reference/#routestxt
			"", // No URL YET
			color,
			"000000",
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

	for _, schedule := range city.ScheduleDef {
		any_day_of_week_set := schedule.DaysOfWeek[0] == 1 || schedule.DaysOfWeek[1] == 1 || schedule.DaysOfWeek[2] == 1 || schedule.DaysOfWeek[3] == 1 || schedule.DaysOfWeek[4] == 1 || schedule.DaysOfWeek[5] == 1 || schedule.DaysOfWeek[6] == 1

		// A day of the week must be specified or this must have holidays set (as holidays must still reference a schedule)
		if any_day_of_week_set || schedule.Holidays {
			calendar_output = append(calendar_output, fmt.Sprintf("%s,%d,%d,%d,%d,%d,%d,%d,%s,%s",
				schedule.Code,
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
				schedule.Code,
				fmt.Sprintf("%04d%02d%02d", holiday.Year, holiday.Month, holiday.Day),
				date_action, // Whether this date was added or not depends on the holiday
			))
		}
	}

	return strings.Join(calendar_output, "\n"), strings.Join(calendar_dates_output, "\n"), nil
}

func (s *MetromanServer) GenerateTripsTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	output := []string{
		"route_id,service_id,trip_id,trip_headsign,direction_id,shape_id",
	}

	for _, route := range city.Routes {
		for schedule_idx, trips := range route.Trips {
			for trip_idx, _ := range trips {
				output = append(output, fmt.Sprintf("%s,%s,%s_trip_%s_%d,%s,%d,shape_%s",
					route.Code,
					route.Schedules[schedule_idx].Code,
					route.Code,
					route.Schedules[schedule_idx].Code,
					trip_idx,
					route.EnglishName,
					route.IdxWithinLine%2, // Noted here as having to be 0 or 1 https://gtfs.org/documentation/schedule/reference/#stopstxt
					route.Code,
				))
			}
		}
	}

	return strings.Join(output, "\n"), nil
}

func (s *MetromanServer) GenerateShapesTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	output := []string{
		"shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence,shape_dist_traveled",
	}

	for _, route := range city.Routes {
		counter := 0
		for station_idx := range len(route.Stations) - 1 {
			coords, exists := route.Line.StationPaths[fmt.Sprintf("%s_%s", route.Stations[station_idx].Code, route.Stations[station_idx+1].Code)]
			if exists {
				// Go forwards
				for i := 0; i < len(coords); i++ {
					output = append(output, fmt.Sprintf("shape_%s,%f,%f,%d,",
						route.Code,
						coords[i].Lat,
						coords[i].Lng,
						counter,
					))
					counter++
				}
			} else {
				coords := route.Line.StationPaths[fmt.Sprintf("%s_%s", route.Stations[station_idx+1].Code, route.Stations[station_idx].Code)]
				// Go backwards
				for i := len(coords) - 1; i >= 0; i-- {
					output = append(output, fmt.Sprintf("shape_%s,%f,%f,%d,",
						route.Code,
						coords[i].Lat,
						coords[i].Lng,
						counter,
					))
					counter++
				}
			}
		}
	}

	return strings.Join(output, "\n"), nil
}

func (s *MetromanServer) GenerateStopTimesTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	output := []string{
		"trip_id,arrival_time,departure_time,stop_id,stop_sequence,timepoint",
	}

	for _, route := range city.Routes {
		for schedule_idx, trips := range route.Trips {
			// Sort trips
			sorted_trips := make([]MetromanTrip, len(trips))
			copy(sorted_trips, trips)
			slices.SortFunc(sorted_trips, func(a MetromanTrip, b MetromanTrip) int {
				return a.Visits[0].ArrivalAndDepartMinutes - b.Visits[0].ArrivalAndDepartMinutes
			})

			for trip_idx, trip := range sorted_trips {
				for i, station_visit := range trip.Visits {
					// We only care about this
					depart_hour := station_visit.ArrivalAndDepartMinutes / 60
					depart_min := station_visit.ArrivalAndDepartMinutes % 60

					output = append(output, fmt.Sprintf("%s_trip_%s_%d,%02d:%02d:00,%02d:%02d:00,%s,%d,%d",
						route.Code,
						route.Schedules[schedule_idx].Code,
						trip_idx,
						depart_hour,
						depart_min,
						depart_hour,
						depart_min,
						station_visit.Station.Code,
						i,
						1, // Timepoints are considered exact
					))
				}
			}
		}
	}

	return strings.Join(output, "\n"), nil
}
