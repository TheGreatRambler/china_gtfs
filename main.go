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
	csv, err := metroman_server.GenerateStopsTXT(TEST_CITY, false)
	if err != nil {
		panic(err)
	}

	// Create build directory if it doesn't exist
	err = os.MkdirAll("build", 0755)
	if err != nil {
		panic(err)
	}

	// Write stops.txt to build directory
	err = os.WriteFile("build/stops.txt", []byte(csv), 0644)
	if err != nil {
		panic(err)
	}
}
