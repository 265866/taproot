package taproot

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/265866/taproot/internal/buildinfo"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
)

// hiddenFlags are upstream flags that do not apply to taproot. They stay
// defined, so upstream's flag code is untouched, but the help text leaves them
// out: Home mode and the TUI's embedded server are refused (CheckServerMode),
// password is hidden upstream too, and the rest log in to or import providers
// the dashboard dropped (Kimi China, Devin, Vertex, SIWC).
var hiddenFlags = map[string]bool{
	"home-jwt":                       true,
	"home-disable-cluster-discovery": true,
	"standalone":                     true,
	"password":                       true,
	"kimi-login":                     true,
	"devin-login":                    true,
	"vertex-import":                  true,
	"vertex-import-prefix":           true,
	"siwc-bind":                      true,
}

// VersionLine names this build, for --version and the startup log.
func VersionLine() string {
	return fmt.Sprintf("taproot %s (commit %s, built %s)", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
}

// HandleArgs is taproot's command-line hook. cmd/server/main.go calls it once,
// after upstream defines its flags and before flag.Parse. It replaces the help
// text with taproot's and serves --version, which prints VersionLine and exits 0.
// If upstream or a plugin defines its own version flag, --version is theirs.
// When a server run's --config names a file that does not exist, it writes the
// default config.yaml there and taproot.yaml where Setup will look for it (see
// writeDefaultConfig), so a first start needs no files; a failed write exits 1.
// A run with neither --config nor ./config.yaml exits 1 with a hint.
func HandleArgs(args []string) {
	handleArgs(flag.CommandLine, args, os.Stdout, os.Exit)
}

func handleArgs(fs *flag.FlagSet, args []string, stdout io.Writer, exit func(int)) {
	owned := fs.Lookup("version") == nil
	if owned {
		fs.Bool("version", false, "Print the version and exit")
	}
	fs.Usage = func() { writeUsage(fs) }
	values, parsed := scanFlags(fs, args[1:])
	if owned {
		if requested, err := strconv.ParseBool(values["version"]); err == nil && requested {
			_, _ = fmt.Fprintln(stdout, VersionLine())
			exit(0)
			return
		}
	}
	if !parsed {
		return
	}
	getenv := startupEnv()
	if configFromElsewhere(getenv) {
		return
	}
	path := values["config"]
	if f := fs.Lookup("config"); path == "" && f != nil {
		path = f.DefValue
	}
	if path == "" {
		if !discoverRun(values) && missing("config.yaml") {
			log.Error("taproot: no --config flag, and no config.yaml in the working directory. Run taproot --config <dir>/config.yaml: on its first start taproot writes a default config.yaml and taproot.yaml in <dir>.")
			exit(1)
		}
		return
	}
	if !serverRun(values) {
		return
	}
	if err := writeDefaultConfig(path, getenv); err != nil {
		log.Errorf("taproot: write the default config files: %v", err)
		exit(1)
	}
}

// serverFlags are the flags a server run may set. Any other flag set to a
// value other than false, such as a login, -tui, or a plugin's command,
// selects a run that does not start the server.
var serverFlags = map[string]bool{
	"config":                         true,
	"local-model":                    true,
	"no-browser":                     true,
	"oauth-callback-port":            true,
	"password":                       true,
	"management-base-url":            true,
	"standalone":                     true,
	"home-disable-cluster-discovery": true,
	"discover-timeout":               true,
	"discover-service-type":          true,
	"discover-include":               true,
	"discover-exclude":               true,
}

// serverRun reports whether the flags start the server, the only run that
// writes first-run files.
func serverRun(values map[string]string) bool {
	for name, value := range values {
		if !serverFlags[name] && value != "" && value != "false" {
			return false
		}
	}
	return true
}

// discoverRun reports whether main runs LAN discovery, which reads no config.
func discoverRun(values map[string]string) bool {
	for _, name := range []string{"discover", "discover-json"} {
		if v, ok := values[name]; ok && v != "false" {
			return true
		}
	}
	return false
}

// configFromElsewhere reports whether upstream ignores a missing --config
// file: it reads a Postgres, git, or object store, contacts Home (which
// CheckServerMode refuses), or waits for a config in a cloud deploy.
func configFromElsewhere(getenv func(string) string) bool {
	if getenv("DEPLOY") == "cloud" {
		return true
	}
	for _, key := range []string{"PGSTORE_DSN", "GITSTORE_GIT_URL", "OBJECTSTORE_ENDPOINT", "HOME_JWT"} {
		if strings.TrimSpace(getenv(key)) != "" || strings.TrimSpace(getenv(strings.ToLower(key))) != "" {
			return true
		}
	}
	return false
}

// startupEnv reads a variable as main sees it once it has loaded .env from
// the working directory, which it does after HandleArgs: a variable set in
// the process environment wins over .env.
func startupEnv() func(string) string {
	dotenv, err := godotenv.Read(".env")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.WithError(err).Warn("taproot: read .env")
	}
	return func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return dotenv[key]
	}
}

func missing(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// scanFlags returns the value of each flag that flag.Parse would set, reading
// the arguments the way the flag package does: it stops at "--" and at the
// first argument that is not a flag, and skips the value of a non-boolean
// flag. A boolean flag without a value is "true". parsed is false when
// flag.Parse will fail or print the help instead: at a flag that is not
// defined (-h and -help included) or a last flag that lacks its value.
func scanFlags(fs *flag.FlagSet, args []string) (values map[string]string, parsed bool) {
	values = map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) < 2 || arg[0] != '-' || arg == "--" {
			return values, true
		}
		name := strings.TrimPrefix(arg[1:], "-")
		name, value, hasValue := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil {
			return values, false
		}
		if !hasValue {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				value = "true"
			} else if i+1 < len(args) {
				i++
				value = args[i]
			} else {
				return values, false
			}
		}
		values[name] = value
	}
	return values, true
}

func writeUsage(fs *flag.FlagSet) {
	out := fs.Output()
	_, _ = fmt.Fprintln(out, "Usage: taproot [flags]")
	_, _ = fmt.Fprintln(out, "\nWithout a login, import, -tui, or -discover flag, taproot starts the server. It reads taproot.yaml from beside --config, or from TAPROOT_CONFIG. When the file --config names does not exist, the server writes a default config.yaml there, and a taproot.yaml.")
	_, _ = fmt.Fprintln(out, "\nFlags:")
	fs.VisitAll(func(f *flag.Flag) {
		if hiddenFlags[f.Name] {
			return
		}
		line := "  -" + f.Name
		name, usage := flag.UnquoteUsage(f)
		if name != "" {
			line += " " + name
		}
		line += "\n    " + strings.ReplaceAll(usage, "\n", "\n    ")
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && !strings.Contains(usage, "(default ") {
			line += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		_, _ = fmt.Fprintln(out, line)
	})
}
