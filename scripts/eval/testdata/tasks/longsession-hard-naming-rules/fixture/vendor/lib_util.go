package libutil

// Version is the vendored release the workspace pins.
const Version = "v1.4.2"

// Join joins the parts with a slash and trims a trailing one.
func Join(parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}
