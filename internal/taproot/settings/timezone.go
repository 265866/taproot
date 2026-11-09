package settings

import (
	"strings"
	"time"
)

// localZoneName names the zone Go loads as time.Local, following its lookup
// on Unix: $TZ when set (an empty value means UTC, a leading colon is
// ignored, and an absolute path names a zone file), else the target of the
// /etc/localtime symlink. It returns "" when the zone has no IANA name, such
// as a copied /etc/localtime, or when the name does not load.
func localZoneName(lookupEnv func(string) (string, bool), readlink func(string) (string, error)) string {
	tz, set := lookupEnv("TZ")
	tz = strings.TrimPrefix(tz, ":")
	var name string
	switch {
	case set && tz == "":
		name = "UTC"
	case !set || tz == "/etc/localtime":
		if target, err := readlink("/etc/localtime"); err == nil {
			name = zoneFromPath(target)
		}
	case strings.HasPrefix(tz, "/"):
		name = zoneFromPath(tz)
	default:
		name = tz
	}
	if name == "" {
		return ""
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ""
	}
	return name
}

// zoneFromPath turns a zone file path such as /usr/share/zoneinfo/Etc/UTC
// into its IANA name, or "" when the path is not under a zoneinfo directory.
func zoneFromPath(path string) string {
	const dir = "zoneinfo/"
	i := strings.LastIndex(path, dir)
	if i < 0 {
		return ""
	}
	name := path[i+len(dir):]
	for _, prefix := range []string{"posix/", "right/"} {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}
