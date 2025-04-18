package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the current version of the application
	Version = "dev"
	// Commit is the git commit hash
	Commit = "unknown"
	// BuildDate is the date the binary was built
	BuildDate = "unknown"
)

// Info holds version information
type Info struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	Platform  string
}

// Get returns the version information
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String returns a formatted string of version information
func (i Info) String() string {
	return fmt.Sprintf("rcptpixie version %s (commit: %s, built: %s, %s, %s)",
		i.Version, i.Commit, i.BuildDate, i.GoVersion, i.Platform)
}
