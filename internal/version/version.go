package version

import "time"

// Current is the version string of this CLI.
// Bump this value whenever you cut a new release.
const Current = "0.2.0"

// LatestURL points to the VERSION file on GitHub
// that the CLI uses to check for newer releases.
// The file should contain a single version string, e.g. 0.1.1
const LatestURL = "https://raw.githubusercontent.com/vinitran/smart-git/main/VERSION"

// Info holds metadata about the current and latest versions.
type Info struct {
	Current   string
	Latest    string
	CheckedAt time.Time
}
