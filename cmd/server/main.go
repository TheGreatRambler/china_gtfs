package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"tgrcode.com/china_gtfs"
)

func main() {
	// Flags
	flag_server := flag.Bool("server", false, "Start HTTP server")
	flag_load_all := flag.Bool("metroman-load-all", false, "Preload all cities (no server)")
	flag_preload_with_server := flag.Bool("metroman-preload-all", false, "Preload cities before starting server")
	flag_port := flag.String("port", "8080", "Port to listen on for the HTTP server")
	flag_city_csv := flag.String("city-csv", "baidu_city_uid_to_city.csv", "Path to baidu_city_uid_to_city.csv")
	flag.Parse()

	// -------------------------------------------------------
	// Behavior rules matching your usage block
	// -------------------------------------------------------

	if !*flag_server && !*flag_load_all {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s --server [--port=8080] [--metroman-preload-all]\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  %s --metroman-load-all\n", filepath.Base(os.Args[0]))
		os.Exit(1)
	}

	if *flag_load_all && *flag_preload_with_server {
		fmt.Fprintf(os.Stderr, "Error: --metroman-load-all cannot be combined with --metroman-preload-all\n")
		os.Exit(1)
	}

	// preload-only mode (do not run server)
	if *flag_load_all {
		china_gtfs_server, err := china_gtfs.CreateServer()
		if err != nil {
			log.Fatalf("Error creating GTFS server: %v", err)
		}

		generate_gtfs := makeGtfsGenerator(china_gtfs_server)

		if err := metromanLoadAll(*flag_city_csv, generate_gtfs); err != nil {
			log.Fatalf("Error preloading cities: %v", err)
		}
		return
	}

	// server mode (optional preload)
	china_gtfs_server, err := china_gtfs.CreateServer()
	if err != nil {
		log.Fatalf("Error creating GTFS server: %v", err)
	}

	generate_gtfs := makeGtfsGenerator(china_gtfs_server)

	if *flag_preload_with_server {
		if err := metromanLoadAll(*flag_city_csv, generate_gtfs); err != nil {
			log.Fatalf("Error preloading cities: %v", err)
		}
	}

	startServer(generate_gtfs, *flag_port)
}

// -------------------------------------------------------
// GTFS generator factory
// -------------------------------------------------------
func makeGtfsGenerator(china_gtfs_server *china_gtfs.ChinaGTFSServer) func(code string) ([]byte, error) {
	return func(code string) ([]byte, error) {
		version, err := china_gtfs_server.MetromanGetCityVersion(code)
		if err != nil {
			return nil, fmt.Errorf("getting version for %s: %w", code, err)
		}

		gtfs_filename := fmt.Sprintf("%s.%s.gtfs.zip", code, version)
		gtfs_path := filepath.Join("build", gtfs_filename)

		if _, err := os.Stat(gtfs_path); err == nil {
			return os.ReadFile(gtfs_path)
		}

		if err := china_gtfs_server.MetromanLoadCity(code); err != nil {
			return nil, fmt.Errorf("loading city %s: %w", code, err)
		}

		raw_zip, err := china_gtfs_server.MetromanGetRawZip(code)
		if err != nil {
			return nil, fmt.Errorf("getting raw zip for %s: %w", code, err)
		}

		os.MkdirAll("backup", 0755)
		backup_filename := fmt.Sprintf("%s.%s.metroman.zip", code, version)
		backup_path := filepath.Join("backup", backup_filename)
		os.WriteFile(backup_path, raw_zip, 0644)

		gtfs_zip, err := china_gtfs_server.MetromanGenerateGTFSZip(code, false)
		if err != nil {
			return nil, fmt.Errorf("generating GTFS zip for %s: %w", code, err)
		}

		os.MkdirAll("build", 0755)
		os.WriteFile(gtfs_path, gtfs_zip, 0644)

		return gtfs_zip, nil
	}
}

// -------------------------------------------------------
// HTTP server for TransitLand (DMFR)
// -------------------------------------------------------
func startServer(generate_gtfs func(code string) ([]byte, error), port string) {
	router := mux.NewRouter()

	router.HandleFunc("/{code}.gtfs.zip", func(w http.ResponseWriter, r *http.Request) {
		code := mux.Vars(r)["code"]

		gtfs_data, err := generate_gtfs(code)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error generating GTFS: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.gtfs.zip\"", code))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(gtfs_data)))
		w.Write(gtfs_data)
	})

	addr := ":" + port
	log.Printf("Starting server at %s", addr)
	log.Fatal(http.ListenAndServe(addr, router))
}

// -------------------------------------------------------
// Preload cities from "baidu_city_uid_to_city.csv"
// -------------------------------------------------------
func metromanLoadAll(csv_path string, generate_gtfs func(code string) ([]byte, error)) error {
	f, err := os.Open(csv_path)
	if err != nil {
		return fmt.Errorf("opening CSV: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)

	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("reading header: %w", err)
	}

	metroman_idx := -1
	for i, col := range header {
		if col == "metroman_code" {
			metroman_idx = i
			break
		}
	}
	if metroman_idx == -1 {
		return fmt.Errorf("CSV missing metroman_code column")
	}

	row_index := 0
	for {
		// Sleep a bit on every iteration as to not overload MetroMan
		time.Sleep(time.Second * 1)

		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading row %d: %w", row_index, err)
		}
		row_index++

		if len(record) <= metroman_idx || record[metroman_idx] == "" {
			continue
		}

		code := record[metroman_idx]
		log.Printf("Preloading %s...", code)

		if _, err := generate_gtfs(code); err != nil {
			log.Printf("Error loading %s: %v", code, err)
		}
	}

	return nil
}
