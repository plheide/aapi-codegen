// aapi-codegen is the AsyncAPI 3.x → Go code generator CLI.
//
// Minimal invocation: `aapi-codegen SPEC.asyncapi.yaml`. Defaults under
// this minimal form: output = ./types.gen.go in cwd; package derived
// from the output directory's basename (with the version-folder rule
// described on defaultPackageName below). All defaults are overridable
// via flags or the optional -config YAML file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/plheide/aapi-codegen/pkg/codegen"
)

func main() {
	var (
		configPath string
		pkg        string
		out        string
	)
	flag.StringVar(&configPath, "config", "", "Optional YAML config (overrides for advanced cases — most invocations don't need one).")
	flag.StringVar(&pkg, "package", "", "Go package name. Default: derived from the output directory.")
	flag.StringVar(&out, "o", "", "Output Go file path. Default: ./types.gen.go in cwd.")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: aapi-codegen [-config FILE] [-package PKG] [-o OUT.go] SPEC.asyncapi.yaml\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := buildConfig(configPath, pkg, out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aapi-codegen:", err)
		os.Exit(1)
	}

	if _, err := codegen.Generate(flag.Arg(0), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "aapi-codegen:", err)
		os.Exit(1)
	}
}

// buildConfig assembles a codegen.Config from CLI flags + optional YAML.
// Precedence: explicit flag > YAML config field > defaults.
//
// Defaults kick in only when neither flag nor YAML supplies the field,
// so a config file that intentionally pins (say) Output isn't silently
// overridden by a defaults rule.
func buildConfig(configPath, pkg, out string) (codegen.Config, error) {
	cfg := codegen.Config{Package: pkg, Output: out}
	if configPath != "" {
		yaml, err := codegen.LoadConfiguration(configPath)
		if err != nil {
			return codegen.Config{}, err
		}
		// Flag values supersede YAML when set; that's the longstanding
		// expectation for tools that accept both.
		yaml.MergeFlags(pkg, out, nil)
		cfg = yaml.ToCodegenConfig()
	}
	if cfg.Output == "" {
		cfg.Output = "./types.gen.go"
	}
	if cfg.Package == "" {
		cfg.Package = defaultPackageName(cfg.Output)
	}
	return cfg, nil
}

// versionDirRegexp matches "v1", "v2", "v10" — the conventional
// version-suffix folder layout common to repos that version schemas
// with a `vN` directory under each contract (e.g.
// .../job-message/v1/). When the basename of the output directory
// matches this, the parent directory's basename is more meaningful
// than the version itself, and we concatenate.
var versionDirRegexp = regexp.MustCompile(`^v\d+$`)

// defaultPackageName derives a Go package identifier from the output
// path. Rules:
//
//   - Take the basename of dir(outputPath).
//   - If that basename is a version folder ("v1", "v2"), prepend the
//     parent directory's basename so e.g. .../job-message/v1/types.gen.go
//     yields "jobmessagev1" rather than the bare "v1".
//   - Sanitize: drop non-alphanumerics, lowercase. "job-message" →
//     "jobmessage"; "v1" stays "v1".
//
// Yields the conventional Go package name for repos that follow the
// `<contract>/vN/` directory convention without any configuration. When
// the rule produces something undesirable, users override with
// -package PKG or set it in -config YAML.
func defaultPackageName(outputPath string) string {
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return "main"
	}
	dir := filepath.Dir(abs)
	base := filepath.Base(dir)
	if versionDirRegexp.MatchString(base) {
		parent := filepath.Base(filepath.Dir(dir))
		if parent != "" && parent != "/" && parent != "." {
			base = sanitizeIdentifier(parent) + base
		}
	}
	return sanitizeIdentifier(base)
}

// sanitizeIdentifier strips non-alphanumeric runes and lowercases the
// remainder. Sufficient for the directory-derived inputs above; not
// general-purpose.
func sanitizeIdentifier(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
