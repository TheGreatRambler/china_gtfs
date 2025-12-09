# China-GTFS

![banner](./img/banner.png)

GTFS feeds for various Chinese public transit agencies and modes, obtained from reverse engineering Chinese apps and implementing their file formats, routines, and network endpoints. Try querying 48 different Chinese cities on the public OpenTripPlanner instance at [tgrcode.com/china_gtfs/route/](https://tgrcode.com/china_gtfs/route/).

# GTFS Files
* agency.txt
* routes.txt
* stops.txt
* calendar.txt
* shapes.txt
* trips.txt
* calendar_dates.txt
* stop_times.txt

# Implemented Apps
* [MetroMan](https://www.metroman.cn/) (subway/metro for 48 cities)
* [Baidu Maps](https://map.baidu.com/) (universal IDs and linking)

# Testing
To test all available GTFS feeds in OpenTripPlanner:
1. `go run ./cmd/server --metroman-load-all` to generate all GTFS feeds and write them to directory `build`
2. Download an OpenStreetMap PBF, like [China](https://download.geofabrik.de/asia/china.html) or [Beijing](https://download.geofabrik.de/asia/china/beijing.html), and save to directory `build`
3. Run `./build_otp.sh`, which uses the latest OpenTripPlanner Docker container to generate a `graph.obj` file
4. Run `./test_otp.sh`, which uses the latest OpenTripPlanner Docker container to expose a routing frontend at `http://localhost:8080`

# Coming
* Fares support
* Automated testing against existing apps
* Additional apps implemented