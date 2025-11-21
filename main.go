package main

import (
	"os"

	"tgrcode.com/baidu_client"
	"tgrcode.com/metroman_client"
)

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

	TEST_CITY := "bj"

	err = metroman_server.LoadCity(TEST_CITY)
	if err != nil {
		panic(err)
	}

	// Create build directory if it doesn't exist
	err = os.MkdirAll("build", 0755)
	if err != nil {
		panic(err)
	}

	stops_txt, err := metroman_server.GenerateStopsTXT(TEST_CITY, false)
	if err != nil {
		panic(err)
	}

	// Write stops.txt to build directory
	err = os.WriteFile("build/stops.txt", []byte(stops_txt), 0644)
	if err != nil {
		panic(err)
	}

	fare_rules_txt, fare_attributes_txt, err := metroman_server.GenerateFaresTXT(TEST_CITY, false)
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

	// For now effectively hardcoded
	agency_txt := metroman_server.GenerateAgencyTXT(TEST_CITY)

	// Write agency.txt to build directory
	err = os.WriteFile("build/agency.txt", []byte(agency_txt), 0644)
	if err != nil {
		panic(err)
	}

	routes_txt, err := metroman_server.GenerateRoutesTXT(TEST_CITY)
	if err != nil {
		panic(err)
	}

	// Write routes.txt to build directory
	err = os.WriteFile("build/routes.txt", []byte(routes_txt), 0644)
	if err != nil {
		panic(err)
	}

	calendar_txt, calendar_dates_txt, err := metroman_server.GenerateCalendarTXT(TEST_CITY)
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
}
