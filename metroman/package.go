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
	"tgrcode.com/china_gtfs/common"
)

type MetromanServer struct {
	CityZips      map[string][]byte
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
	StationPaths map[string][]common.Coordinate
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
		CityZips:      make(map[string][]byte),
		Cities:        make(map[string]*MetromanCity),
		ZipDateLookup: versions_lookup,
	}, nil
}

func (s *MetromanServer) SetBaiduServer(baidu_server *baidu_client.BaiduServer) {
	s.BaiduServer = baidu_server
}

func (s *MetromanServer) GetCityVersion(code string) (string, error) {
	zip_date, ok := s.ZipDateLookup[code]
	if !ok {
		return "", fmt.Errorf("city with code '%s' has not been loaded", code)
	}
	return zip_date, nil
}

func (s *MetromanServer) LoadCity(code string) error {
	// Get zip date, erroring if this city does not exist
	zip_date, ok := s.ZipDateLookup[code]
	if !ok {
		return fmt.Errorf("city with code '%s' has not been loaded", code)
	}

	// Download zip (without headers)
	url := fmt.Sprintf("https://metroman.oss-cn-hangzhou.aliyuncs.com/app/metromanandroid/v202005/%s/%s.zip", code, zip_date)
	zip_resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer zip_resp.Body.Close()

	zip, err := io.ReadAll(zip_resp.Body)
	if err != nil {
		return err
	}

	s.CityZips[code] = zip

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
	uno_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/uno.csv", zip_prefix))
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

			corrected_coord := common.GCJ02ToWGS84(common.Coordinate{
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
				StationPaths:    map[string][]common.Coordinate{},
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
	line_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/line.csv", zip_prefix))
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
	way_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/way.csv", zip_prefix))
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
	fare_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/fare.csv", zip_prefix))
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
			stations = routes_by_code[strings.Split(fare_record[1], "|")[0]].Stations
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
	holiday_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/holiday.csv", zip_prefix))
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
	schedule_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/schedule.csv", zip_prefix))
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
	wayschedule_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/wayschedule.csv", zip_prefix))
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
		schedule_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/%s.csv", zip_prefix, route.Code))
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
		if len(schedule_csv_lines) != (len(route.Stations)-1)*len(route.Schedules) {
			// Path when trains end in the middle and we have to intelligently handle that
			schedule_station_arrivals_departures := []map[int]int{make(map[int]int)}
			station_idx := 0
			last_depart_min := 0

			for schedule_record_line_idx, schedule_record_line := range schedule_csv_lines {
				schedule_record := strings.Split(schedule_record_line, ",")

				depart_min, _ := strconv.ParseInt(schedule_record[0], 10, 0)
				arrive_next_min, _ := strconv.ParseInt(schedule_record[1], 10, 0)

				if int(depart_min) < last_depart_min {
					if len(schedule_station_arrivals_departures) == len(route.Stations)-1 {
						// A set of trips exists for this schedule. We have reached a new schedule
						// Save this one, clear and reset
						station_arrivals_departures = append(station_arrivals_departures,
							schedule_station_arrivals_departures)

						station_idx = 0
						schedule_station_arrivals_departures = []map[int]int{make(map[int]int)}
					} else {
						station_idx++
						// Always add to the list. This list is wiped so that there are never any extra maps
						schedule_station_arrivals_departures = append(schedule_station_arrivals_departures,
							make(map[int]int))
					}
				}

				schedule_station_arrivals_departures[station_idx][int(arrive_next_min)] = int(depart_min)
				last_depart_min = int(depart_min)

				// If we're the last one add the current map
				if schedule_record_line_idx == len(schedule_csv_lines)-1 {
					station_arrivals_departures = append(station_arrivals_departures,
						schedule_station_arrivals_departures)
				}
			}
		} else {
			// There's one train per schedule. Special case as there's no way of detecting a new station
			csv_idx := 0
			for range route.Schedules {
				schedule_station_arrivals_departures := []map[int]int{}

				for range len(route.Stations) - 1 {
					schedule_record := strings.Split(schedule_csv_lines[csv_idx], ",")

					depart_min, _ := strconv.ParseInt(schedule_record[0], 10, 0)
					arrive_next_min, _ := strconv.ParseInt(schedule_record[1], 10, 0)

					// Only one entry per station
					schedule_station_arrivals_departures = append(schedule_station_arrivals_departures, map[int]int{
						int(arrive_next_min): int(depart_min),
					})

					csv_idx++
				}

				station_arrivals_departures = append(station_arrivals_departures,
					schedule_station_arrivals_departures)
			}
		}

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
	path_latlng_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/path_latlng.csv", zip_prefix))
	if err != nil {
		return nil, fmt.Errorf("could not open path_latlng.csv: %v", err)
	}

	// Read through the CSV
	path_latlng_csv_lines := strings.Split(string(path_latlng_csv_contents), "\r\n")

	all_latlng_coords := []common.Coordinate{}
	for _, path_latlng_record_line := range path_latlng_csv_lines {
		path_latlng_record := strings.Split(path_latlng_record_line, ",")

		lat_raw, _ := strconv.ParseFloat(path_latlng_record[0], 64)
		lng_raw, _ := strconv.ParseFloat(path_latlng_record[1], 64)

		// Add a new coord
		all_latlng_coords = append(all_latlng_coords, common.GCJ02ToWGS84(common.Coordinate{
			Lat: lat_raw,
			Lng: lng_raw,
		}))
	}

	// Read in the mappings for line and stations to their indices (inclusive) in the list of coords
	path_rail_csv_contents, err := common.ReadFileFromZip(payload_reader, fmt.Sprintf("%s/path_rail.csv", zip_prefix))
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
			line.StationPaths[path_code] = []common.Coordinate{}
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

func (s *MetromanServer) GetRawZip(code string) ([]byte, error) {
	zip, ok := s.CityZips[code]
	if !ok {
		return []byte{}, fmt.Errorf("city with code '%s' has not been loaded", code)
	}
	return zip, nil
}

func CSVToMatrixInt(payload_reader *zip.Reader, filename string) ([][]int, error) {
	matrix_csv_contents, err := common.ReadFileFromZip(payload_reader, filename)
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

	var buf bytes.Buffer
	csv_writer := csv.NewWriter(&buf)

	// Header
	if err := csv_writer.Write([]string{
		"stop_id", "stop_code", "stop_name", "tts_stop_name", "stop_desc",
		"stop_lat", "stop_lon", "zone_id", "stop_url", "location_type",
		"parent_station", "stop_timezone", "wheelchair_boarding",
		"level_id", "platform_code", "stop_access",
	}); err != nil {
		return "", err
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
			//fmt.Printf("Try typing autocomplete fallback for %s\n", station.EnglishName)

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

		record := []string{
			station_code,           // stop_id (potentially internal to MetroMan)
			station.SimplifiedName, // stop_code (potentially not true for cities other than Beijing)
			station.EnglishName,    // stop_name
			"",                     // tts_stop_name
			"",                     // stop_desc
			fmt.Sprintf("%f", station.Lat),
			fmt.Sprintf("%f", station.Lng),
			fmt.Sprintf("zone_%s", station_code), // Peculiarity of GTFS: fares cannot be specified by distance, this must be done instead
			url,
			"0",             // location_type
			"",              // parent_station
			"Asia/Shanghai", // stop_timezone
			"0",             // wheelchair_boarding
			"",              // level_id
			"",              // platform_code
			"",              // stop_access
		}

		if err := csv_writer.Write(record); err != nil {
			return "", err
		}
	}

	csv_writer.Flush()
	if err := csv_writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *MetromanServer) GenerateFaresTXT(code string, full bool) (string, string, error) {
	city, exists := s.Cities[code]
	if !exists {
		return "", "", fmt.Errorf("city %v not loaded", code)
	}

	// You pay 3 generally if you enter and exit the subway: https://www.reddit.com/r/shanghai/comments/18lrq74/enter_exit_same_metro_station_what_happens/

	// Will be using GTFS fares v1 and generating every station to station pair in one direction
	// TODO only do one direction optimization if the fare matrix is mirrored, for now assuming it

	var rules_buf bytes.Buffer
	var attrs_buf bytes.Buffer
	rules_writer := csv.NewWriter(&rules_buf)
	attrs_writer := csv.NewWriter(&attrs_buf)

	// fare_rules.txt header
	if err := rules_writer.Write([]string{
		"fare_id", "route_id", "origin_id", "destination_id", "contains_id",
	}); err != nil {
		return "", "", err
	}

	// fare_attributes.txt header
	if err := attrs_writer.Write([]string{
		"fare_id", "price", "currency_type", "payment_method", "transfers", "agency_id", "transfer_duration",
	}); err != nil {
		return "", "", err
	}

	for i, fare_matrix_stations := range city.FareMatrixStations {
		for x, start_station := range fare_matrix_stations {
			for y, end_station := range fare_matrix_stations {
				// I am allowing ALL station pairs so transit apps don't choke
				// if end_station.Index >= start_station.Index

				fare_id := fmt.Sprintf("fare_%s_%s",
					city.Stations[start_station.Index].Code,
					city.Stations[end_station.Index].Code,
				)

				// rules
				if err := rules_writer.Write([]string{
					fare_id,
					"", // route_id
					fmt.Sprintf("zone_%s", city.Stations[start_station.Index].Code),
					fmt.Sprintf("zone_%s", city.Stations[end_station.Index].Code),
					"", // contains_id
				}); err != nil {
					return "", "", err
				}

				// attributes
				if err := attrs_writer.Write([]string{
					fare_id,
					fmt.Sprintf("%d", (*city.FareMatrices[i])[x][y]),
					"CNY",
					"1", // payment_method
					"0", // transfers
					"",  // agency_id
					"",  // transfer_duration
				}); err != nil {
					return "", "", err
				}
			}
		}
	}

	rules_writer.Flush()
	attrs_writer.Flush()

	if err := rules_writer.Error(); err != nil {
		return "", "", err
	}
	if err := attrs_writer.Error(); err != nil {
		return "", "", err
	}

	return rules_buf.String(), attrs_buf.String(), nil
}

func (s *MetromanServer) GenerateAgencyTXT(code string) string {
	var buf bytes.Buffer
	csv_writer := csv.NewWriter(&buf)

	_ = csv_writer.Write([]string{
		"agency_id", "agency_name", "agency_url", "agency_timezone", "agency_lang", "agency_phone",
	})
	_ = csv_writer.Write([]string{
		code,
		fmt.Sprintf("China-GTFS %s", s.BaiduServer.CityUIDMappingsByMetromanCode[code].EnglishName),
		"https://tgrcode.com/",
		"Asia/Shanghai",
		"zh",
		"",
	})

	csv_writer.Flush()
	return buf.String()
}

func (s *MetromanServer) GenerateRoutesTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	var buf bytes.Buffer
	csv_writer := csv.NewWriter(&buf)

	if err := csv_writer.Write([]string{
		"agency_id", "route_id", "route_short_name", "route_long_name",
		"route_type", "route_url", "route_color", "route_text_color",
	}); err != nil {
		return "", err
	}

	for _, route := range city.Routes {
		if len(route.Trips) > 0 {
			// No hashtag in color
			color := ""
			if len(route.Line.Color) > 0 {
				color = route.Line.Color[1:]
			}

			if err := csv_writer.Write([]string{
				city_code,
				route.Code,
				route.SimplifiedName,
				route.EnglishName,
				"2", // https://gtfs.org/documentation/schedule/reference/#routestxt
				"",  // No URL YET
				color,
				"000000",
			}); err != nil {
				return "", err
			}
		}
	}

	csv_writer.Flush()
	if err := csv_writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *MetromanServer) GenerateCalendarTXT(city_code string) (string, string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", "", fmt.Errorf("city %v not loaded", city_code)
	}

	var cal_buf bytes.Buffer
	var dates_buf bytes.Buffer
	cal_writer := csv.NewWriter(&cal_buf)
	dates_writer := csv.NewWriter(&dates_buf)

	if err := cal_writer.Write([]string{
		"service_id", "monday", "tuesday", "wednesday", "thursday",
		"friday", "saturday", "sunday", "start_date", "end_date",
	}); err != nil {
		return "", "", err
	}
	if err := dates_writer.Write([]string{
		"service_id", "date", "exception_type",
	}); err != nil {
		return "", "", err
	}

	for _, schedule := range city.ScheduleDef {
		any_day_of_week_set := schedule.DaysOfWeek[0] == 1 || schedule.DaysOfWeek[1] == 1 || schedule.DaysOfWeek[2] == 1 || schedule.DaysOfWeek[3] == 1 || schedule.DaysOfWeek[4] == 1 || schedule.DaysOfWeek[5] == 1 || schedule.DaysOfWeek[6] == 1

		// A day of the week must be specified or this must have holidays set (as holidays must still reference a schedule)
		if any_day_of_week_set || schedule.Holidays {
			if err := cal_writer.Write([]string{
				schedule.Code,
				fmt.Sprintf("%d", schedule.DaysOfWeek[0]),
				fmt.Sprintf("%d", schedule.DaysOfWeek[1]),
				fmt.Sprintf("%d", schedule.DaysOfWeek[2]),
				fmt.Sprintf("%d", schedule.DaysOfWeek[3]),
				fmt.Sprintf("%d", schedule.DaysOfWeek[4]),
				fmt.Sprintf("%d", schedule.DaysOfWeek[5]),
				fmt.Sprintf("%d", schedule.DaysOfWeek[6]),
				fmt.Sprintf("%04d%02d%02d", 2000, 1, 1),   // Day in the past
				fmt.Sprintf("%04d%02d%02d", 9999, 12, 31), // Day in the future
			}); err != nil {
				return "", "", err
			}
		}

		date_action := 2 // Remove the date
		if schedule.Holidays {
			date_action = 1 // Add the date
		}

		// Note every single holiday day
		for _, holiday := range city.Holidays {
			if err := dates_writer.Write([]string{
				schedule.Code,
				fmt.Sprintf("%04d%02d%02d", holiday.Year, holiday.Month, holiday.Day),
				fmt.Sprintf("%d", date_action),
			}); err != nil {
				return "", "", err
			}
		}
	}

	cal_writer.Flush()
	dates_writer.Flush()

	if err := cal_writer.Error(); err != nil {
		return "", "", err
	}
	if err := dates_writer.Error(); err != nil {
		return "", "", err
	}

	return cal_buf.String(), dates_buf.String(), nil
}

func (s *MetromanServer) GenerateTripsTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	var buf bytes.Buffer
	csv_writer := csv.NewWriter(&buf)

	if err := csv_writer.Write([]string{
		"route_id", "service_id", "trip_id", "trip_headsign", "direction_id", "shape_id",
	}); err != nil {
		return "", err
	}

	for _, route := range city.Routes {
		if len(route.Trips) > 0 {
			for schedule_idx, trips := range route.Trips {
				for trip_idx := range trips {
					trip_id := fmt.Sprintf("%s_trip_%s_%d",
						route.Code,
						route.Schedules[schedule_idx].Code,
						trip_idx,
					)

					if err := csv_writer.Write([]string{
						route.Code,
						route.Schedules[schedule_idx].Code,
						trip_id,
						route.EnglishName,
						fmt.Sprintf("%d", route.IdxWithinLine%2), // 0 or 1
						fmt.Sprintf("shape_%s", route.Code),
					}); err != nil {
						return "", err
					}
				}
			}
		}
	}

	csv_writer.Flush()
	if err := csv_writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *MetromanServer) GenerateShapesTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	var buf bytes.Buffer
	csv_writer := csv.NewWriter(&buf)

	if err := csv_writer.Write([]string{
		"shape_id", "shape_pt_lat", "shape_pt_lon", "shape_pt_sequence", "shape_dist_traveled",
	}); err != nil {
		return "", err
	}

	for _, route := range city.Routes {
		if len(route.Trips) > 0 {
			counter := 0
			for station_idx := range len(route.Stations) - 1 {
				coords, exists := route.Line.StationPaths[fmt.Sprintf("%s_%s", route.Stations[station_idx].Code, route.Stations[station_idx+1].Code)]
				if exists {
					// Go forwards
					for i := 0; i < len(coords); i++ {
						if err := csv_writer.Write([]string{
							fmt.Sprintf("shape_%s", route.Code),
							fmt.Sprintf("%f", coords[i].Lat),
							fmt.Sprintf("%f", coords[i].Lng),
							fmt.Sprintf("%d", counter),
							"",
						}); err != nil {
							return "", err
						}
						counter++
					}
				} else {
					coords := route.Line.StationPaths[fmt.Sprintf("%s_%s", route.Stations[station_idx+1].Code, route.Stations[station_idx].Code)]
					// Go backwards
					for i := len(coords) - 1; i >= 0; i-- {
						if err := csv_writer.Write([]string{
							fmt.Sprintf("shape_%s", route.Code),
							fmt.Sprintf("%f", coords[i].Lat),
							fmt.Sprintf("%f", coords[i].Lng),
							fmt.Sprintf("%d", counter),
							"",
						}); err != nil {
							return "", err
						}
						counter++
					}
				}
			}
		}
	}

	csv_writer.Flush()
	if err := csv_writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *MetromanServer) GenerateStopTimesTXT(city_code string) (string, error) {
	city, exists := s.Cities[city_code]
	if !exists {
		return "", fmt.Errorf("city %v not loaded", city_code)
	}

	var buf bytes.Buffer
	csv_writer := csv.NewWriter(&buf)

	if err := csv_writer.Write([]string{
		"trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence", "timepoint",
	}); err != nil {
		return "", err
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

					trip_id := fmt.Sprintf("%s_trip_%s_%d",
						route.Code,
						route.Schedules[schedule_idx].Code,
						trip_idx,
					)
					time_str := fmt.Sprintf("%02d:%02d:00", depart_hour, depart_min)

					if err := csv_writer.Write([]string{
						trip_id,
						time_str,
						time_str,
						station_visit.Station.Code,
						fmt.Sprintf("%d", i),
						"1", // Timepoints are considered exact
					}); err != nil {
						return "", err
					}
				}
			}
		}
	}

	csv_writer.Flush()
	if err := csv_writer.Error(); err != nil {
		return "", err
	}

	return buf.String(), nil
}
