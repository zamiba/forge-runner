package runner

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Dependencies are the things that must already be on the machine for a runner
// to work, keyed by OS name — "Linux", "Windows", "Mac". By OS and not by
// platform, because a shared library's name does not change with the
// architecture even though the file does.
//
// This is deliberately not forge's `dependencies`, which is a PATH check for
// commands and doubles as its `run` allowlist. A shared library is neither a
// command nor something a build invokes, so forge has no way to express it, and
// the failure surfaces as the program's own loader error after a successful
// install.
type Dependencies map[string]OSDependencies

// OSDependencies is what one OS needs.
type OSDependencies struct {
	// Libraries are shared libraries by soname, as the loader looks for them:
	// "libfontconfig.so.1", not "fontconfig".
	//
	// **Declare what somebody might plausibly lack, not everything the binary
	// links.** libc, libm, libstdc++ and libgcc_s are on every glibc desktop, so
	// listing them adds noise and invents failures — on musl there is no
	// libc.so.6 to find and the program may still run. The binary already
	// carries its full DT_NEEDED list and the loader already reads it; what a
	// declaration uniquely buys is a warning *before* a 70MB download, and that
	// is only worth giving for the ones that are actually missing sometimes.
	Libraries []string `json:"libraries,omitempty"`

	// Hint is what to tell a person, because a soname is not installable. Package
	// names differ per distribution and no amount of inspecting the binary will
	// reveal them, so this is the one part that has to be written by hand.
	Hint string `json:"hint,omitempty"`
}

// For returns the dependencies for a platform, accepting either an OS name or a
// full platform name — "Linux" or "Linux-x64".
func (d Dependencies) For(platform string) OSDependencies {
	if dep, ok := d[platform]; ok {
		return dep
	}
	if os, _, found := strings.Cut(platform, "-"); found {
		return d[os]
	}
	return OSDependencies{}
}

// MissingLibraries returns the declared sonames that cannot be found on this
// machine, in the order declared.
//
// It answers before anything is installed, which is the whole point: telling
// somebody they need fontconfig is worth more before a 70MB download than after
// it. It is therefore a name search and not a verdict — see VerifyLinked for the
// verdict, which needs the installed binary.
//
// Returns nothing on an OS where this does not apply, rather than guessing:
// Windows resolves DLLs beside the executable and macOS ships frameworks inside
// the bundle, so a missing-library search there would report absences that are
// not absences.
func MissingLibraries(libs []string) []string {
	if runtime.GOOS != "linux" || len(libs) == 0 {
		return nil
	}
	known := linkerCache()
	var missing []string
	for _, lib := range libs {
		if !known[lib] && !onDisk(lib) {
			missing = append(missing, lib)
		}
	}
	return missing
}

// linkerCache reads the sonames ldconfig knows about. An empty map when
// ldconfig is absent — musl systems have no such cache — and onDisk covers that
// case instead.
func linkerCache() map[string]bool {
	out, err := exec.Command("ldconfig", "-p").Output()
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		// "\tlibfontconfig.so.1 (libc6,x86-64) => /lib/x86_64-linux-gnu/libfontconfig.so.1"
		line := strings.TrimSpace(sc.Text())
		if name, _, ok := strings.Cut(line, " "); ok && strings.Contains(name, ".so") {
			known[name] = true
		}
	}
	return known
}

// onDisk looks in the directories a loader searches by default, plus whatever
// LD_LIBRARY_PATH adds. It is the fallback for a machine with no ldconfig, and
// it is deliberately a short list: a false "present" is harmless here because
// the launch is verified again against the real binary, while a false "missing"
// would nag somebody about a library they have.
func onDisk(soname string) bool {
	dirs := []string{
		"/lib", "/lib64", "/usr/lib", "/usr/lib64",
		"/usr/local/lib", "/usr/local/lib64",
	}
	dirs = append(dirs, filepath.SplitList(os.Getenv("LD_LIBRARY_PATH"))...)
	// The multiarch directories, which is where a Debian or Ubuntu system keeps
	// nearly everything.
	for _, base := range []string{"/lib", "/usr/lib"} {
		if entries, err := os.ReadDir(base); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.Contains(e.Name(), "-linux-") {
					dirs = append(dirs, filepath.Join(base, e.Name()))
				}
			}
		}
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, soname)); err == nil {
			return true
		}
	}
	return false
}

// VerifyLinked asks the dynamic loader whether an installed program can actually
// start, and returns the complaints it makes.
//
// This exists because a declared list of names cannot catch the failure that
// actually bites. Gopher64 needs GLIBC_2.39 and GLIBCXX_3.4.31: on an older
// distribution libc.so.6 and libstdc++.so.6 are both present, a name search says
// everything is fine, and the program still will not start. Only the loader
// knows, because only the loader checks symbol versions.
//
// So this runs ldd, which *is* the loader, and reports what it says. It is a
// complement to MissingLibraries and not a replacement: this one needs the
// binary, so it can only answer after an install, and a declaration can warn
// before one.
//
// Nothing is reported on a non-Linux host, or when ldd is absent. A check that
// cannot run must not invent a pass or a failure, so the caller gets an empty
// list and carries on — the program's own error is then the fallback, which is
// where we were before any of this.
//
// ldd runs the loader against the binary, which can execute code in it. That is
// acceptable here only because the alternative on offer is executing the same
// binary deliberately a moment later.
func VerifyLinked(exePath string) []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	cmd := exec.Command("ldd", exePath)
	out, _ := cmd.CombinedOutput() // a non-zero exit is normal when something is wrong

	var problems []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasSuffix(line, "=> not found"):
			// "libfontconfig.so.1 => not found"
			name, _, _ := strings.Cut(line, " ")
			problems = append(problems, name+" is missing")
		case strings.Contains(line, "not found (required by"):
			// "/lib/libc.so.6: version `GLIBC_2.39' not found (required by …)"
			// The library is present and too old, which is the case a name
			// search cannot see.
			if _, rest, ok := strings.Cut(line, "version `"); ok {
				if version, _, ok := strings.Cut(rest, "'"); ok {
					problems = append(problems, version+" is older than this build needs")
					continue
				}
			}
			problems = append(problems, line)
		}
	}
	return dedupe(problems)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
