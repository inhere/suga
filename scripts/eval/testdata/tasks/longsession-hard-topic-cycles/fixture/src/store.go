package cycle

// Put stores a row in the map.
func Put(m map[string]int, region string, units int) {
	m[region] += units
}
