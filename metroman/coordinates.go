package metroman_client

import (
	"math"
	"strings"
)

// Define constants for geo types
type GeoType int

const (
	GEO_TYPE_AREA  GeoType = 0
	GEO_TYPE_LINE  GeoType = 1
	GEO_TYPE_POINT GeoType = 2
)

type Coordinate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type Mercator struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type GeoDiff struct {
	Type   GeoType
	Points []Mercator
}

// Decode combined geo diff
func DecodeCombinedGeoDiff(encoded string) []GeoDiff {
	var result []GeoDiff
	elems := strings.Split(encoded, "|")
	for _, elem := range elems {
		decoded := DecodeGeoDiff(elem)
		if len(decoded.Points) != 0 {
			result = append(result, decoded)
		}
	}
	return result
}

// Decode single geo diff
func DecodeGeoDiff(encoded string) GeoDiff {
	if len(encoded) == 0 {
		return GeoDiff{}
	}

	geo_type_char := encoded[0]           // First character denotes geo type
	geo_type := GetGeoType(geo_type_char) // Map first character to geo type
	geo_data := encoded[1:]               // The rest is the geo data

	var points []Mercator
	var current_point Mercator
	index := 0

	for index < len(geo_data) {
		char := geo_data[index]

		if char == '=' || char == '-' {
			// Process 13-character blocks
			// This is always the first
			if len(geo_data)-index < 13 {
				return GeoDiff{}
			}
			block := geo_data[index : index+13]
			if !Parse13Block(block, &current_point) {
				return GeoDiff{}
			}
			index += 13

			// Save current point
			points = append(points, Mercator{
				X: current_point.X,
				Y: current_point.Y,
			})
		} else if char == ';' {
			// Reset for next
			current_point = Mercator{}
			index++
		} else {
			// Process 8-character blocks
			if len(geo_data)-index < 8 {
				return GeoDiff{}
			}
			block := geo_data[index : index+8]
			if !Parse8Block(block, &current_point) {
				return GeoDiff{}
			}
			index += 8

			// Save current point
			points = append(points, Mercator{
				X: current_point.X,
				Y: current_point.Y,
			})
		}
	}

	// Adjust all points by dividing by 100
	for i := range points {
		points[i].X /= 100
		points[i].Y /= 100
	}

	return GeoDiff{
		Type:   geo_type,
		Points: points,
	}
}

// Helper function to map the first character to geo type
func GetGeoType(char byte) GeoType {
	switch char {
	case '.':
		return GEO_TYPE_POINT
	case '-':
		return GEO_TYPE_LINE
	case '*':
		return GEO_TYPE_AREA
	default:
		return -1
	}
}

// Helper function to parse a 13 byte block and add to current point
func Parse13Block(block string, current_point *Mercator) bool {
	var x, y int64

	// Parse x and y from block
	for i := 0; i < 6; i++ {
		char_code_x := ParseChar(block[1+i])
		char_code_y := ParseChar(block[7+i])
		if char_code_x < 0 || char_code_y < 0 {
			return false
		}

		x += int64(char_code_x) << (6 * i)
		y += int64(char_code_y) << (6 * i)
	}

	*current_point = Mercator{
		X: float64(x),
		Y: float64(y),
	}
	return true
}

// Helper function to parse a 8 byte block and add to current point
// Baidu Maps uses delta encoding for lines
func Parse8Block(block string, current_point *Mercator) bool {
	const MAX_DELTA_VALUE = 1 << 23

	var delta_x, delta_y int64

	// Parse delta_x and delta_y from the block
	for i := 0; i < 4; i++ {
		char_code_x := ParseChar(block[i])
		char_code_y := ParseChar(block[4+i])
		if char_code_x < 0 || char_code_y < 0 {
			return false
		}

		delta_x += int64(char_code_x) << (6 * i)
		delta_y += int64(char_code_y) << (6 * i)
	}

	// Ensure deltas are within valid range
	if delta_x > MAX_DELTA_VALUE {
		delta_x = MAX_DELTA_VALUE - delta_x
	}
	if delta_y > MAX_DELTA_VALUE {
		delta_y = MAX_DELTA_VALUE - delta_y
	}

	// Update the current point by adding the deltas to the previous point
	current_point.X += float64(delta_x)
	current_point.Y += float64(delta_y)

	return true
}

// Helper function to convert a character to a numeric value
func ParseChar(char byte) int {
	switch {
	case char >= 'A' && char <= 'Z':
		return int(char - 'A')
	case char >= 'a' && char <= 'z':
		return int(char - 'a' + 26)
	case char >= '0' && char <= '9':
		return int(char - '0' + 52)
	case char == '+':
		return 62
	case char == '/':
		return 63
	default:
		return -1
	}
}

var mcband = []float64{
	12890594.86, 8362377.87,
	5591021, 3481989.83, 1678043.12, 0,
}

var mc2ll = [][]float64{
	{1.410526172116255e-8, 0.00000898305509648872, -1.9939833816331,
		200.9824383106796, -187.2403703815547, 91.6087516669843,
		-23.38765649603339, 2.57121317296198, -0.03801003308653,
		17337981.2},
	{-7.435856389565537e-9, 0.000008983055097726239,
		-0.78625201886289, 96.32687599759846, -1.85204757529826,
		-59.36935905485877, 47.40033549296737, -16.50741931063887,
		2.28786674699375, 10260144.86},
	{-3.030883460898826e-8, 0.00000898305509983578, 0.30071316287616,
		59.74293618442277, 7.357984074871, -25.38371002664745,
		13.45380521110908, -3.29883767235584, 0.32710905363475,
		6856817.37},
	{-1.981981304930552e-8, 0.000008983055099779535, 0.03278182852591,
		40.31678527705744, 0.65659298677277, -4.44255534477492,
		0.85341911805263, 0.12923347998204, -0.04625736007561,
		4482777.06},
	{3.09191371068437e-9, 0.000008983055096812155, 0.00006995724062,
		23.10934304144901, -0.00023663490511, -0.6321817810242,
		-0.00663494467273, 0.03430082397953, -0.00466043876332,
		2555164.4},
	{2.890871144776878e-9, 0.000008983055095805407, -3.068298e-8,
		7.47137025468032, -0.00000353937994, -0.02145144861037,
		-0.00001234426596, 0.00010322952773, -0.00000323890364,
		826088.5},
}

// Convertor is a helper function that handles coordinate conversion.
func MercatorConvertor(px, py float64, table []float64) (float64, float64) {
	x := table[0] + table[1]*math.Abs(px)
	d := math.Abs(py) / table[9]
	y := table[2] +
		table[3]*d +
		table[4]*d*d +
		table[5]*d*d*d +
		table[6]*d*d*d*d +
		table[7]*d*d*d*d*d +
		table[8]*d*d*d*d*d*d

	return x * Sign(px), y * Sign(py)
}

// Sign returns -1 if the value is negative, 1 otherwise.
func Sign(val float64) float64 {
	if math.Signbit(val) {
		return -1
	}
	return 1
}

// Inverse takes a Mercator coordinate and returns a Coordinate with latitude and longitude.
func BaiduMercatorInverse(mercator Mercator) Coordinate {
	y_abs := math.Abs(mercator.Y)

	var table []float64
	for i := 0; i < len(mcband); i++ {
		if y_abs >= mcband[i] {
			table = mc2ll[i]
			break
		}
	}

	lng, lat := MercatorConvertor(mercator.X, mercator.Y, table)

	return Coordinate{
		Lat: lat,
		Lng: lng,
	}
}

const xPi = 3.14159265358979324 * 3000.0 / 180.0

// Converts BD-09 to GCJ-02 coordinates
func BD09ToGCJ02(coord Coordinate) Coordinate {
	x := coord.Lng - 0.0065
	y := coord.Lat - 0.006
	z := math.Sqrt(x*x+y*y) - 0.00002*math.Sin(y*xPi)
	theta := math.Atan2(y, x) - 0.000003*math.Cos(x*xPi)

	lng := z * math.Cos(theta)
	lat := z * math.Sin(theta)

	return Coordinate{
		Lat: lat,
		Lng: lng,
	}
}

// Converts GCJ-02 to BD-09 coordinates.
func BD09FromGCJ02(coord Coordinate) Coordinate {
	x := coord.Lng
	y := coord.Lat
	z := math.Sqrt(x*x+y*y) + 0.00002*math.Sin(y*xPi)
	theta := math.Atan2(y, x) + 0.000003*math.Cos(x*xPi)

	lng := z*math.Cos(theta) + 0.0065
	lat := z*math.Sin(theta) + 0.006

	return Coordinate{
		Lat: lat,
		Lng: lng,
	}
}

const axis = 6378245.0
const offset = 0.00669342162296594323

// Delta calculates the difference between WGS-84 and GCJ-02 coordinates.
func GCJ02Delta(lng, lat float64) (float64, float64) {
	d_lat := TransformLat(lng-105.0, lat-35.0)
	d_lng := TransformLon(lng-105.0, lat-35.0)
	rad_lat := lat / 180.0 * math.Pi
	magic := math.Sin(rad_lat)
	magic = 1 - offset*magic*magic
	sqrt_magic := math.Sqrt(magic)
	d_lat = (d_lat * 180.0) / ((axis * (1 - offset)) / (magic * sqrt_magic) * math.Pi)
	d_lng = (d_lng * 180.0) / (axis / sqrt_magic * math.Cos(rad_lat) * math.Pi)
	return d_lng, d_lat
}

// OutOfChina checks if the coordinates are outside China.
func OutOfChina(lng, lat float64) bool {
	if lng < 72.004 || lng > 137.8347 {
		return true
	}
	if lat < 0.8293 || lat > 55.8271 {
		return true
	}
	return false
}

// TransformLat performs the latitude transformation.
func TransformLat(x, y float64) float64 {
	ret := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	ret += (160.0*math.Sin(y/12.0*math.Pi) + 320*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return ret
}

// TransformLon performs the longitude transformation.
func TransformLon(x, y float64) float64 {
	ret := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	ret += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return ret
}

// ToWGS84 converts GCJ-02 to WGS-84.
func GCJ02ToWGS84(coord Coordinate) Coordinate {
	lng := coord.Lng
	lat := coord.Lat
	if !OutOfChina(lng, lat) {
		d_lng, d_lat := GCJ02Delta(lng, lat)
		lng = lng - d_lng
		lat = lat - d_lat
	}
	return Coordinate{
		Lat: lat,
		Lng: lng,
	}
}

// FromWGS84 converts WGS-84 to GCJ-02.
func GCJ02FromWGS84(coord Coordinate) Coordinate {
	lng := coord.Lng
	lat := coord.Lat
	if !OutOfChina(lng, lat) {
		d_lng, d_lat := GCJ02Delta(lng, lat)
		lng = lng + d_lng
		lat = lat + d_lat
	}
	return Coordinate{
		Lat: lat,
		Lng: lng,
	}
}
