package geo

func LatLong(code int) (float64, float64, bool) {
	latLong, ok := data[code]
	if !ok {
		return 0, 0, false
	}
	return latLong[0], latLong[1], true
}
