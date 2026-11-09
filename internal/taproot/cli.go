package taproot

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/265866/taproot/internal/buildinfo"
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
// When --config names a file that does not exist, it writes the default
// config.yaml there and taproot.yaml beside it (see writeDefaultConfig), so a
// first start needs no files; a failed write exits 1.
func HandleArgs(args []string) {
	handleArgs(flag.CommandLine, args, os.Stdout, os.Exit)
}

func handleArgs(fs *flag.FlagSet, args []string, stdout io.Writer, exit func(int)) {
	owned := fs.Lookup("version") == nil
	if owned {
		fs.Bool("version", false, "Print the version and exit")
	}
	fs.Usage = func() { writeUsage(fs) }
	if len(args) < 2 {
		return
	}
	values, help := scanFlags(fs, args[1:])
	if owned {
		if requested, err := strconv.ParseBool(values["version"]); err == nil && requested {
			_, _ = fmt.Fprintln(stdout, VersionLine())
			exit(0)
			return
		}
	}
	if path := values["config"]; path != "" && !help {
		if err := writeDefaultConfig(path); err != nil {
			log.Errorf("taproot: write the default config files: %v", err)
			exit(1)
		}
	}
}

// scanFlags returns the value of each flag that flag.Parse would set, reading
// the arguments the way the flag package does: it stops at "--", at the first
// argument that is not a flag, and at a flag that is not defined (flag.Parse
// then fails, or prints the help for -h and -help, which help reports), and
// skips the value of a non-boolean flag. A boolean flag without a value is
// "true".
func scanFlags(fs *flag.FlagSet, args []string) (values map[string]string, help bool) {
	values = map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) < 2 || arg[0] != '-' || arg == "--" {
			return values, false
		}
		name := strings.TrimPrefix(arg[1:], "-")
		name, value, hasValue := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil {
			return values, name == "h" || name == "help"
		}
		if !hasValue {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				value = "true"
			} else if i+1 < len(args) {
				i++
				value = args[i]
			}
		}
		values[name] = value
	}
	return values, false
}

func writeUsage(fs *flag.FlagSet) {
	out := fs.Output()
	_, _ = fmt.Fprintln(out, "Usage: taproot [flags]")
	_, _ = fmt.Fprintln(out, "\nWithout a login, import, -tui, or -discover flag, taproot starts the server. It reads taproot.yaml from beside --config, or from TAPROOT_CONFIG.")
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
