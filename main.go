package main

import (
	"archive/zip"
	"fmt"
	"os"

	"tgrcode.com/baidu_client"
	"tgrcode.com/metroman_client"
)

var DEBUG = false

func add_file_to_zip(zip_writer *zip.Writer, filename string, contents []byte) error {
	header := &zip.FileHeader{
		Name:   filename,
		Method: zip.Deflate,
	}

	file_writer, err := zip_writer.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = file_writer.Write(contents)
	return err
}

func main() {
	metroman_server, err := metroman_client.CreateServer()
	if err != nil {
		panic(err)
	}

	baidu_server, err := baidu_client.CreateServer()
	if err != nil {
		panic(err)
	}

	metroman_server.SetBaiduServer(baidu_server)

	for _, city := range []string{"bj"} {
		err = metroman_server.LoadCity(city)
		if err != nil {
			panic(err)
		}

		stops_txt, err := metroman_server.GenerateStopsTXT(city, false)
		if err != nil {
			panic(err)
		}

		fare_rules_txt, fare_attributes_txt, err := metroman_server.GenerateFaresTXT(city, false)
		if err != nil {
			panic(err)
		}

		// For now effectively hardcoded
		agency_txt := metroman_server.GenerateAgencyTXT(city)

		routes_txt, err := metroman_server.GenerateRoutesTXT(city)
		if err != nil {
			panic(err)
		}

		calendar_txt, calendar_dates_txt, err := metroman_server.GenerateCalendarTXT(city)
		if err != nil {
			panic(err)
		}

		trips_txt, err := metroman_server.GenerateTripsTXT(city)
		if err != nil {
			panic(err)
		}

		shapes_txt, err := metroman_server.GenerateShapesTXT(city)
		if err != nil {
			panic(err)
		}

		stop_times_txt, err := metroman_server.GenerateStopTimesTXT(city)
		if err != nil {
			panic(err)
		}

		// Create build directory if it doesn't exist
		err = os.MkdirAll("build", 0755)
		if err != nil {
			panic(err)
		}

		if DEBUG {
			// Write stops.txt to build directory
			err = os.WriteFile("build/stops.txt", []byte(stops_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write fare_rules.txt to build directory
			err = os.WriteFile("build/fare_rules.txt", []byte(fare_rules_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write fare_attributes.txt to build directory
			err = os.WriteFile("build/fare_attributes.txt", []byte(fare_attributes_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write agency.txt to build directory
			err = os.WriteFile("build/agency.txt", []byte(agency_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write routes.txt to build directory
			err = os.WriteFile("build/routes.txt", []byte(routes_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write calendar.txt to build directory
			err = os.WriteFile("build/calendar.txt", []byte(calendar_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write calendar_dates.txt to build directory
			err = os.WriteFile("build/calendar_dates.txt", []byte(calendar_dates_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write trips.txt to build directory
			err = os.WriteFile("build/trips.txt", []byte(trips_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write shapes.txt to build directory
			err = os.WriteFile("build/shapes.txt", []byte(shapes_txt), 0644)
			if err != nil {
				panic(err)
			}

			// Write stop_times.txt to build directory
			err = os.WriteFile("build/stop_times.txt", []byte(stop_times_txt), 0644)
			if err != nil {
				panic(err)
			}
		}

		// Create output
		output_zip_file, err := os.Create(fmt.Sprintf("build/%s.gtfs.zip", city))
		if err != nil {
			panic(err)
		}
		defer output_zip_file.Close()

		output_zip_writer := zip.NewWriter(output_zip_file)
		defer output_zip_writer.Close()

		// Add every file to output zip
		add_file_to_zip(output_zip_writer, "stops.txt", []byte(stops_txt))
		//add_file_to_zip(output_zip_writer, "fare_rules.txt", []byte(fare_rules_txt))
		//add_file_to_zip(output_zip_writer, "fare_attributes.txt", []byte(fare_attributes_txt))
		add_file_to_zip(output_zip_writer, "agency.txt", []byte(agency_txt))
		add_file_to_zip(output_zip_writer, "routes.txt", []byte(routes_txt))
		add_file_to_zip(output_zip_writer, "calendar.txt", []byte(calendar_txt))
		add_file_to_zip(output_zip_writer, "calendar_dates.txt", []byte(calendar_dates_txt))
		add_file_to_zip(output_zip_writer, "trips.txt", []byte(trips_txt))
		add_file_to_zip(output_zip_writer, "shapes.txt", []byte(shapes_txt))
		add_file_to_zip(output_zip_writer, "stop_times.txt", []byte(stop_times_txt))
	}
}
