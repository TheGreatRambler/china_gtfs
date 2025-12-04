package china_gtfs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"

	"tgrcode.com/baidu_client"
	"tgrcode.com/metroman_client"
)

type ChinaGTFSServer struct {
	MetromanServer *metroman_client.MetromanServer
	BaiduServer    *baidu_client.BaiduServer
}

func CreateServer() (*ChinaGTFSServer, error) {
	metroman_server, err := metroman_client.CreateServer()
	if err != nil {
		panic(err)
	}

	baidu_server, err := baidu_client.CreateServer()
	if err != nil {
		panic(err)
	}

	metroman_server.SetBaiduServer(baidu_server)

	return &ChinaGTFSServer{
		MetromanServer: metroman_server,
		BaiduServer:    baidu_server,
	}, nil
}

func (s *ChinaGTFSServer) MetromanLoadCity(city string) error {
	return s.MetromanServer.LoadCity(city)
}

func (s *ChinaGTFSServer) MetromanGetCityVersion(city string) (string, error) {
	return s.MetromanServer.GetCityVersion(city)
}

func addFileToZip(zip_writer *zip.Writer, filename string, contents []byte) error {
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

func writeDebugFile(dir string, filename string, contents []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(dir, filename)
	return os.WriteFile(path, contents, 0o644)
}

func (s *ChinaGTFSServer) MetromanGetRawZip(city string) ([]byte, error) {
	return s.MetromanServer.GetRawZip(city)
}

func (s *ChinaGTFSServer) MetromanGenerateGTFSZip(city string, debug bool) ([]byte, error) {

	stops_txt, err := s.MetromanServer.GenerateStopsTXT(city, false)
	if err != nil {
		return nil, err
	}

	agency_txt := s.MetromanServer.GenerateAgencyTXT(city)

	routes_txt, err := s.MetromanServer.GenerateRoutesTXT(city)
	if err != nil {
		return nil, err
	}

	calendar_txt, calendar_dates_txt, err := s.MetromanServer.GenerateCalendarTXT(city)
	if err != nil {
		return nil, err
	}

	trips_txt, err := s.MetromanServer.GenerateTripsTXT(city)
	if err != nil {
		return nil, err
	}

	shapes_txt, err := s.MetromanServer.GenerateShapesTXT(city)
	if err != nil {
		return nil, err
	}

	stop_times_txt, err := s.MetromanServer.GenerateStopTimesTXT(city)
	if err != nil {
		return nil, err
	}

	// --------------------------------------------------------
	// Debug output
	// --------------------------------------------------------

	if debug {
		debug_dir := "debug"

		writeDebugFile(debug_dir, "stops.txt", []byte(stops_txt))
		writeDebugFile(debug_dir, "agency.txt", []byte(agency_txt))
		writeDebugFile(debug_dir, "routes.txt", []byte(routes_txt))
		writeDebugFile(debug_dir, "calendar.txt", []byte(calendar_txt))
		writeDebugFile(debug_dir, "calendar_dates.txt", []byte(calendar_dates_txt))
		writeDebugFile(debug_dir, "trips.txt", []byte(trips_txt))
		writeDebugFile(debug_dir, "shapes.txt", []byte(shapes_txt))
		writeDebugFile(debug_dir, "stop_times.txt", []byte(stop_times_txt))
	}

	// --------------------------------------------------------
	// Build ZIP
	// --------------------------------------------------------

	output_buf := new(bytes.Buffer)
	zip_writer := zip.NewWriter(output_buf)

	addFileToZip(zip_writer, "stops.txt", []byte(stops_txt))
	addFileToZip(zip_writer, "agency.txt", []byte(agency_txt))
	addFileToZip(zip_writer, "routes.txt", []byte(routes_txt))
	addFileToZip(zip_writer, "calendar.txt", []byte(calendar_txt))
	addFileToZip(zip_writer, "calendar_dates.txt", []byte(calendar_dates_txt))
	addFileToZip(zip_writer, "trips.txt", []byte(trips_txt))
	addFileToZip(zip_writer, "shapes.txt", []byte(shapes_txt))
	addFileToZip(zip_writer, "stop_times.txt", []byte(stop_times_txt))

	zip_writer.Close()

	return output_buf.Bytes(), nil
}
