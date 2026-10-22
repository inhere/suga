package cycle

// Row is one report line.
type Row struct {
	Region string
	Units  int
}

// Sum adds the units of every row.
func Sum(rows []Row) int {
	total := 0
	for _, r := range rows {
		total += r.Units
	}
	return total
}

// Filter keeps the rows of one region.
func Filter(rows []Row, region string) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if r.Region == region {
			out = append(out, r)
		}
	}
	return out
}
